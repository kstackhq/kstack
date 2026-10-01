// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Notices: how a background task ended, told to the model on a user message.
// A finished task whose row has no notified_at is a waiting notice; it rides the
// chat's next question, and an exit also starts a turn of its own. An agent's
// notice is its report.
package chatsvc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kstackhq/kstack/sidecar/internal/llm"
	"github.com/kstackhq/kstack/sidecar/internal/tools"
)

// errNothingToTell is a kick that finds no notice that starts a turn: it starts
// nothing.
var errNothingToTell = errors.New("chatsvc: no notice that starts a turn is waiting")

// noticeLineMax is how many characters of a line a notice keeps.
const noticeLineMax = 200

// nothingToSay is a failed agent's error when its run succeeded with no report.
const nothingToSay = "it had nothing to say"

// waitingNotice is a finished task whose notice has not reached the model, with
// the call that started it, and agentDescription, the description of the Agent
// call whose subagent made that call, empty for the answer's own. agent marks an
// agent's task, whose run gave report and runError; underSend, that the turn
// whose Agent call started it was a user's send.
type waitingNotice struct {
	id, toolUseID, path, status, stoppedBy string
	exitCode                               *int
	description, command, agentDescription string
	agent, underSend                       bool
	report, runError                       string
}

// noticeOf is the notice a waiting task gives the model. A completed agent's
// report goes through tools.Fit with a saver that answers the task's file, which
// already holds it, so a long report is previewed and names the file and
// building a notice writes nothing. Any other agent's file is empty, so its
// notice names none.
func noticeOf(w waitingNotice) llm.TaskNotice {
	n := llm.TaskNotice{
		Kind: llm.TaskCommand, ID: w.id, ToolUseID: w.toolUseID, OutputFile: w.path, Status: llm.TaskStatus(w.status),
		StoppedBy: w.stoppedBy, ExitCode: w.exitCode,
		Description: noticeLine(w.description), Command: noticeLine(w.command), AgentDescription: noticeLine(w.agentDescription),
	}
	if !w.agent {
		return n
	}
	n.Kind = llm.TaskAgent
	switch w.status {
	case taskCompleted:
		n.Result = tools.Fit(func(string) (string, bool) { return w.path, true }, "", w.report, "", 0, "report")
	case taskFailed:
		n.OutputFile, n.Error = "", noticeLine(w.runError)
		if n.Error == "" {
			n.Error = nothingToSay
		}
	default:
		n.OutputFile = ""
	}
	return n
}

// noticeBlocks is each waiting task's notice as a block, in the order they ended.
func noticeBlocks(waiting []waitingNotice) []llm.Block {
	out := make([]llm.Block, 0, len(waiting))
	for _, w := range waiting {
		out = append(out, llm.TaskNotificationBlock(noticeOf(w)))
	}
	return out
}

// withNotices is a question with the notices riding it: after its context block,
// which is always a question's first, and ahead of its text.
func withNotices(question []llm.Block, waiting []waitingNotice) []llm.Block {
	at := 0
	if len(question) > 0 && question[0].Type == llm.BlockContext {
		at = 1
	}
	out := append([]llm.Block{}, question[:at]...)
	out = append(out, noticeBlocks(waiting)...)
	return append(out, question[at:]...)
}

// startsTurn is whether any waiting notice starts a turn: an exit, or the end of
// an agent a user's send launched. Agent is not gated, so an agent a turn the
// sidecar started launched rides the next question instead: otherwise its end
// could start a turn that launches another, with no one approving anything.
func startsTurn(waiting []waitingNotice) bool {
	for _, w := range waiting {
		switch {
		case w.status == taskExited:
			return true
		case w.agent && w.underSend && (w.status == taskCompleted || w.status == taskFailed):
			return true
		}
	}
	return false
}

// noticeLine is what a notice keeps of the model's text: its first line with
// anything on it, cut to noticeLineMax characters, `…` marking a cut only when
// more than whitespace was cut. The transcript's descriptionLine is the same rule.
func noticeLine(s string) string {
	s = strings.TrimLeftFunc(s, unicode.IsSpace)
	head, rest := s, ""
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		head, rest = s[:i], s[i:]
	}
	if utf8.RuneCountInString(head) > noticeLineMax {
		r := []rune(head)
		head, rest = string(r[:noticeLineMax]), string(r[noticeLineMax:])+rest
	}
	head = strings.TrimRightFunc(head, unicode.IsSpace)
	if strings.TrimFunc(rest, unicode.IsSpace) != "" {
		return head + "…"
	}
	return head
}

// takeNotices puts the chat's waiting notices on the question and marks them
// told, inside the send's transaction.
func (s *service) takeNotices(ctx context.Context, st stmts, chatID ChatID, question []llm.Block, at time.Time) ([]llm.Block, error) {
	waiting, err := waitingNotices(ctx, st, chatID, s.tools)
	if err != nil || len(waiting) == 0 {
		return question, err
	}
	if err := markNotified(ctx, st, chatID, at); err != nil {
		return nil, err
	}
	return withNotices(question, waiting), nil
}

// kick starts a turn on the chat's waiting notices when one starts a turn
// (startsTurn) and its slot is free. It runs after a task's end and after a turn
// settles succeeded, so a slot held now kicks again when its turn settles. Any refusal
// leaves the notices waiting for the next question or the next exit.
func (s *service) kick(chatID ChatID) {
	if s.turnOf(chatID) != nil {
		return
	}
	err := s.startNoticeTurn(chatID)
	switch {
	// A full chat stays full on its model until the user picks another, so a line
	// per finished task would say nothing new.
	case err == nil, errors.Is(err, errNothingToTell), errors.Is(err, ErrTurnInFlight),
		errors.Is(err, ErrChatGone), errors.Is(err, ErrStopping), errors.Is(err, ErrChatContextFull):
	default:
		slog.Info("a background task's end started no turn; its notice waits for the next question", "chat", chatID, "err", err)
	}
}

// startNoticeTurn is a send without a sender: a question of the waiting notices
// alone, no request key and no cluster card, on what the chat's last answer ran
// on, with every check a send makes. It carries a context block only when the
// switch moved since the newest one, so the model knows where its commands run. One transaction reserves the turn, files
// the message, marks the notices told and moves the chat up its list.
func (s *service) startNoticeTurn(chatID ChatID) error {
	if err := s.enter(); err != nil {
		return err
	}
	defer s.wg.Done()
	ctx := s.ctx
	at := normalizeTime(s.now())

	var (
		t         *turn
		assistant ChatMessage
	)
	err := s.store.InTx(ctx, func(st stmts) error {
		waiting, err := waitingNotices(ctx, st, chatID, s.tools)
		if err != nil {
			return err
		}
		if !startsTurn(waiting) {
			return errNothingToTell
		}
		providerID, modelID, effort, err := lastAnswerRun(ctx, st, chatID)
		if err != nil {
			return err
		}
		target, err := s.llmSvc.Resolve(providerID, modelID, effort)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrBadRequest, err)
		}
		disabled, err := s.checkChat(ctx, st, &chatID, "")
		if err != nil {
			return err
		}
		newest, err := newestContext(ctx, st, chatID)
		if err != nil {
			return err
		}
		// A notice turn renders no card, so a moved switch reaches the model by
		// re-sending the newest context with its Sandbox section replaced.
		var question []llm.Block
		if replaced := s.withSandboxReplaced(newest, disabled); replaced != newest {
			question = []llm.Block{llm.ContextBlock(replaced)}
		}
		if err := roomFor(ctx, st, chatID, target, s.boxFor(target)); err != nil {
			return err
		}
		if t, err = s.reserveTurn(chatID, newRunID(), target); err != nil {
			return err
		}
		t.outsideSandbox = disabled
		if err := markNotified(ctx, st, chatID, at); err != nil {
			return err
		}
		if err := touchConversation(ctx, st, chatID, at); err != nil {
			return err
		}
		assistant, err = s.writeTurnRows(ctx, st, t, "", effort, withNotices(question, waiting), at)
		return err
	})
	if err != nil {
		if t != nil {
			s.abandonTurn(t)
		}
		return err
	}
	s.startTurn(t, assistant)
	s.notify(messagesKey(chatID))
	s.notify(conversationsKey)
	return nil
}
