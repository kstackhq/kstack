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

package runproxy

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
)

// maxQueued is how many requests may wait for the one being decided, as many
// as the cluster's writes.
const maxQueued = 8

// What Decide answers a request it forwards nothing for.
const (
	refusedUnshowable = "kstack: this request cannot be shown as it would be sent, so it was not sent. Run the command outside the sandbox."
	refusedQuery      = "kstack: this request's query does not parse, so it cannot be shown as it would be sent."
	refusedForm       = "kstack: this request's form body does not parse, so it cannot be shown as it would be sent."
	refusedDenied     = "kstack: the user did not approve this request."
	refusedUnanswered = "kstack: the user did not answer this request."
	refusedUnrecorded = "kstack: this request could not be recorded, so it was not sent."
	refusedQueued     = "kstack: too many requests are waiting on the user. Send one at a time."
	refusedOpen       = "kstack: too many requests are open at once. Close one, then try again."
	unauthorized      = "kstack: unauthorized"
)

// Request is one classified action, held for the user or refused unasked.
type Request struct {
	Action permissions.Action
	Sent   Sent
}

// Sent is the request as the user is shown it.
type Sent struct {
	Host        string // the upstream host dialled
	Method      string
	Path        string // path and raw query, as forwarded
	ContentType string
	Body        []byte // as sent; empty with Unshown
	// Unshown is the provider's one fixed sentence for a body the request does
	// not show.
	Unshown string
	Details []Detail
}

// Detail is one line the provider's classifier read from the request.
type Detail struct {
	Label string
	Value string
}

// Asker puts a request to the user and records one refused unasked. From
// Ask, a context error is a wait that ended with no decision; any other error
// is a request or answer that could not be recorded. From Record, an error is
// a record that could not be written; reason is the decision in the user's
// words.
type Asker interface {
	Ask(ctx context.Context, r Request) (approved bool, err error)
	Record(ctx context.Context, r Request, d permissions.Decision, reason string) error
}

// Outcome is how Decide ended.
type Outcome struct {
	Forwarded bool   // forward was called
	Err       error  // forward's error
	Status    int    // what to answer when nothing was forwarded
	Message   string // the refusal in the user's words, starting "kstack:"
}

func refused(status int, message string) Outcome {
	return Outcome{Status: status, Message: message}
}

// Decide puts req to the user and calls forward once they approve it. It
// keeps the action's class and summary alone and stamps the grant's target,
// so no field that names a cluster's object is decided, recorded or drawn for
// a provider; what a classifier read goes in req.Sent.Details. A grant with no
// asker, and a request that cannot be shown, are refused before any queue. The rest wait for the grant's lock, so the user sees one at a time and
// the provider sees changes in the order decided. The verdict reads no mode and
// no rule, only the session's two flags: a refusal is recorded, and anything
// else asks. A data read gives up the lock once approved, so a long download
// holds no other request; a change holds it until forward returns. Only an
// approval takes a slot and the limiter, so a request waiting on the user holds
// neither.
func (g *Grant) Decide(ctx context.Context, req Request, forward func() error) Outcome {
	req.Action = permissions.Action{Target: g.target, Class: req.Action.Class, Summary: req.Action.Summary}
	if g.asker == nil {
		return refused(http.StatusForbidden, g.refusal)
	}
	if why := unshowable(req.Sent); why != "" {
		return refused(http.StatusForbidden, why)
	}
	if !g.waiters.TryAcquire(1) {
		return refused(http.StatusTooManyRequests, refusedQueued)
	}
	err := g.lock.Acquire(ctx, 1)
	g.waiters.Release(1)
	if err != nil {
		return refused(http.StatusForbidden, refusedUnanswered)
	}
	unlock := sync.OnceFunc(func() { g.lock.Release(1) })
	defer unlock()
	p := permissions.Policy{NoPrompts: g.session.NoPrompts, NoSecretData: g.session.NoSecretData}
	if v, reason := p.Authorize(req.Action); v == permissions.Refuse {
		if err := g.asker.Record(ctx, req, permissions.Denied, reason); err != nil {
			slog.Warn("a refused provider request was not recorded", "err", err)
			return refused(http.StatusForbidden, refusedUnrecorded)
		}
		return refused(http.StatusForbidden, "kstack: "+req.Action.Summary+" is not allowed: "+reason)
	}
	approved, err := g.asker.Ask(ctx, req)
	switch {
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		return refused(http.StatusForbidden, refusedUnanswered)
	case err != nil:
		return refused(http.StatusForbidden, refusedUnrecorded)
	case !approved:
		return refused(http.StatusForbidden, refusedDenied)
	}
	if req.Action.Class == permissions.SecretRead {
		unlock()
	}
	release, status := g.Slot(ctx)
	if status != 0 {
		return refused(status, slotRefusal(status))
	}
	defer release()
	return Outcome{Forwarded: true, Err: forward()}
}

// slotRefusal is the message for a status Slot answered.
func slotRefusal(status int) string {
	if status == http.StatusTooManyRequests {
		return refusedOpen
	}
	return unauthorized
}

// unshowable is why s cannot be put to the user, or "": a body that is not
// UTF-8 text and has no Unshown sentence, or a query or form body
// url.ParseQuery refuses, since a pair that does not parse is dropped and what
// is shown would not be what runs.
func unshowable(s Sent) string {
	if s.Unshown == "" && !utf8.Valid(s.Body) {
		return refusedUnshowable
	}
	_, query, _ := strings.Cut(s.Path, "?")
	if _, err := url.ParseQuery(query); err != nil {
		return refusedQuery
	}
	if s.Unshown == "" && isForm(s.ContentType) {
		if _, err := url.ParseQuery(string(s.Body)); err != nil {
			return refusedForm
		}
	}
	return ""
}

// isForm is whether contentType names a URL-encoded form, read as leniently as
// a provider reads it: parameters and case ignored.
func isForm(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "application/x-www-form-urlencoded")
}
