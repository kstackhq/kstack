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

// One chat's messages. Status is drawn from the row, text from the blocks: a
// streaming, failed or cancelled answer is drawn even with nothing in it, because a
// provider that refuses before its first chunk leaves an empty row whose error is
// all there is to show. An answer's thinking summary is a
// disclosure above its text, and the cluster card a question carries — what the
// app told the model about the cluster on that turn — is one above the question.
// An answer's tool calls are listed under its text off the row, and the one the
// turn waits on is the approval request. A background command's end reaches the
// model as a notice on a user message, drawn as a muted line rather than a bubble.
import { Fragment, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { useMutation } from 'urql';

import { AppLogo } from '@/components/widgets/app-logo';
import { DiffBlock } from '@/components/widgets/diff-block';
import { approvalAnchor } from '@/lib/approval-anchor';
import { Markdown } from '@/components/widgets/markdown';
import { VisibleText } from '@/components/widgets/visible-text';
import { graphql } from '@/gql';
import type { AppMode } from '@/lib/app-mode';
import { useChatOutbox } from '@/lib/chat-outbox';
import {
  actionKindLabel,
  contextOf,
  inFlight,
  isWaitingWrite,
  noticesOf,
  ranSearch,
  searchesOf,
  sourcesOf,
  textOf,
  waitingRequestsOf,
} from '@/lib/chats';
import type { ChatClusterWrite, ChatMessage, ChatToolCall, Source, TaskNotice } from '@/lib/chats';
import type { WatchPhase } from '@/lib/graphql/use-watch-subscription';
import { useHeldStill } from '@/lib/held-still';
import { modelOf, useModels } from '@/lib/models';
import { cutText, descriptionLine } from '@/lib/visible-text';

// How close to the end still counts as reading the end. Fixed, not a fraction of the
// viewport: a threshold that grows with the window yanks a reader back mid-paragraph.
const PIN_SLACK_PX = 32;

/**
 * How long Approve must keep its place on screen before it arms. Requests come
 * and go and answers grow while a request waits, so the page moves around it; a
 * click aimed at one thing must not land on an Approve that slid under the
 * pointer.
 */
export const APPROVE_ARM_MS = 500;

const ApprovalDecideMutation = graphql(`
  mutation ApprovalDecide($id: ApprovalID!, $approve: Boolean!) {
    approvalDecide(id: $id, approve: $approve)
  }
`);

const BackgroundTaskStopMutation = graphql(`
  mutation BackgroundTaskStop($id: ToolCallID!) {
    backgroundTaskStop(id: $id)
  }
`);

type ChatTranscriptProps = {
  messages: ChatMessage[];
  phase: WatchPhase;
  /** The chat this draws, which is also the outbox entry Ask again sends from. */
  chatID: string;
  mode: AppMode;
  /** The chat's own cluster. Undefined while the watch that names it has not answered. */
  clusterID?: string;
  /** How long Approve holds its place before it arms; production's is APPROVE_ARM_MS. */
  approveArmMs?: number;
  /** Whether the machine offers a sandbox. Undefined until the sidecar says. */
  sandboxAvailable?: boolean;
  /** The chat's switch, which Ask again sends as what the user saw. Undefined until the list watch delivers the chat. */
  sandboxDisabled?: boolean;
  /** The chat's sandbox switch is in flight, which Ask again waits for. */
  switching?: boolean;
};

// Every message with something to say about itself: its text, the notices it
// carried, or — for an answer its cap cut off while it was still reasoning — the
// reason it stopped.
function isDrawn(message: ChatMessage): boolean {
  if (message.status !== 'Complete') return true;
  return textOf(message.content) !== '' || message.finishReason !== '' || noticesOf(message.content).length > 0;
}

// The summary of an answer's thinking. Untouched, it is open while the answer is
// still all thinking — the pause it exists to fill — and closed once text arrives. A
// click pins the reader's choice for as long as the message stays mounted.
function Thinking({ summary, stillThinking }: { summary: string; stillThinking: boolean }) {
  const [pinned, setPinned] = useState<boolean>();
  const open = pinned ?? stillThinking;
  return (
    <details open={open} className="mb-2">
      <summary
        className="cursor-pointer text-xs text-muted-foreground select-none"
        onClick={(e) => {
          // The attribute is React's: the click sets the pin, never the DOM.
          e.preventDefault();
          setPinned(!open);
        }}
      >
        Thinking
      </summary>
      <div className="mt-1 border-l-2 border-muted pl-3 text-xs text-muted-foreground">
        <Markdown text={summary} />
      </div>
    </details>
  );
}

// The context a question carries — the cluster card, then the memory notes —
// closed: it is there so the user can see what the provider was told, at the
// message it rode. Plain text, never markdown — its names came from the cluster.
function ClusterContext({ card }: { card: string }) {
  return (
    <details className="mb-2">
      <summary className="cursor-pointer text-xs text-muted-foreground select-none">Context</summary>
      <div className="mt-1 border-l-2 border-muted pl-3 text-xs whitespace-pre-wrap text-muted-foreground">{card}</div>
    </details>
  );
}

// The host a source's URL names, or '' for one that does not parse: the URL is
// web text, and a bad one draws its title alone.
function hostOf(url: string): string {
  try {
    return new URL(url).hostname;
  } catch {
    return '';
  }
}

// Where an answer drawn from the web came from. Always visible, never a
// disclosure; the title and the host are text and the whole URL rides as the
// item's title, since nothing in the webview opens one.
function Sources({ sources }: { sources: Source[] }) {
  return (
    <div className="mt-1 text-xs text-muted-foreground">
      <p>Sources</p>
      <ul className="list-disc pl-4">
        {sources.map((source) => {
          const host = hostOf(source.url);
          return (
            <li key={source.url} title={source.url}>
              {source.title}
              {host !== '' && <span className="opacity-70"> · {host}</span>}
            </li>
          );
        })}
      </ul>
    </div>
  );
}

// What left the machine: how many searches ran and their queries, closed, as
// plain text — they are the model's words.
function Searches({ count, queries }: { count: number; queries: string[] }) {
  // With no query to show, a disclosure would open onto nothing.
  if (queries.length === 0) {
    return <p className="mt-1 text-xs text-muted-foreground">Searched the web · {count}</p>;
  }
  return (
    <details className="mt-1">
      <summary className="cursor-pointer text-xs text-muted-foreground select-none">Searched the web · {count}</summary>
      <div className="mt-1 border-l-2 border-muted pl-3 text-xs whitespace-pre-wrap text-muted-foreground">
        {queries.join('\n')}
      </div>
    </details>
  );
}

// The tag beside a call's summary, off its status alone. A failed call's first
// line is the sidecar's own — its exit code, or the timeout, which carries the
// duration — and the command's output starts on the next, so the tag reads that
// line alone and infers nothing else.
function toolCallTag(call: ChatToolCall): string {
  if (call.background) return backgroundTag(call.background);
  switch (call.status) {
    case 'Failed': {
      const [first] = call.output.split('\n', 1);
      if (first.startsWith('Exit code ')) return `exit ${first.slice('Exit code '.length)}`;
      if (first.startsWith('Command timed out')) return 'timed out';
      return 'failed';
    }
    case 'Denied':
      return 'denied';
    case 'NotRun':
      return 'not run';
    case 'Running':
      return 'running…';
    case 'Interrupted':
      return 'cancelled — may have run';
    default:
      return '';
  }
}

// The tag of a call that started a background task, a command or an agent:
// where the task is, off its own row, since the call itself answered at once.
function backgroundTag(task: NonNullable<ChatToolCall['background']>): string {
  switch (task.status) {
    case 'Running':
      return 'running in background';
    case 'Exited':
      return task.exitCode === null ? 'exited' : `exit ${task.exitCode}`;
    case 'Completed':
      return 'completed';
    case 'Failed':
      return 'failed';
    case 'Stopped':
      return 'stopped';
    default:
      return 'lost';
  }
}

// The user's stop of a running background command or agent. Only an error hands
// the button back: a false answer means nothing was running, and the watch
// brings its end.
function StopTask({ id }: { id: string }) {
  const [, backgroundTaskStop] = useMutation(BackgroundTaskStopMutation);
  const [pressed, setPressed] = useState(false);
  const press = async () => {
    setPressed(true);
    const result = await backgroundTaskStop({ id });
    if (result.error) setPressed(false);
  };
  return (
    <Button type="button" size="xs" variant="outline" className="mt-1" disabled={pressed} onClick={press}>
      Stop
    </Button>
  );
}

// The one reader of a call's summary: the command its action runs, the path it
// reads or writes, the query it searched, else its kind, else the tool's name.
// The sidecar reads the action off the arguments, so nothing here parses them.
function summaryOf(call: ChatToolCall): string {
  const write = writeOf(call.action);
  if (call.action?.command) return call.action.command.text;
  if (call.action?.read) return `Read ${call.action.read.path}`;
  if (write) return `Write ${write.path}`;
  if (call.action?.edit) return `Edit ${call.action.edit.path}`;
  if (call.action?.search) return call.action.search.query === '' ? 'Search' : `Search ${call.action.search.query}`;
  if (call.action?.fetch) return `Fetch ${call.action.fetch.url}`;
  if (call.action?.memory) return `${MEMORY_OPS[call.action.memory.op] ?? 'Memory'} ${call.action.memory.name}`;
  if (call.actionKind) return actionKindLabel(call.actionKind);
  return call.name;
}

// A memory call's summary, by its op.
const MEMORY_OPS: Record<string, string> = { save: 'Save memory', forget: 'Forget memory' };

// What a save kept, on the page where it was kept, as text, since the model may
// have written it from cluster data.
function MemoryKept({ body }: { body: string }) {
  return (
    <pre className="mt-1 border-l-2 border-muted pl-3 font-mono break-all whitespace-pre-wrap">
      <VisibleText text={body} />
    </pre>
  );
}

// The one reader of an action's write. It destructures because the HTML-sink
// lint refuses any `.write`, which is how document.write is spelled.
function writeOf(action: ChatToolCall['action']) {
  const { write } = action ?? { write: null };
  return write;
}

// How much of a command, a write's content or a memory's body the request draws
// whole. Past either, the rest is folded and Approve waits on it being shown:
// what the user has not seen is not approved.
const COMMAND_CHARS = 2000;
const COMMAND_LINES = 24;

// How the request draws a command or a write's content.
const REQUEST_TEXT = 'mt-1 font-mono text-xs break-all whitespace-pre-wrap';

// A text cutText split, spelled: the head, the rest once shown, and while it is
// not, a button saying how much is held back. The counts are memoized, since a
// write's rest can run to megabytes and the transcript re-renders per frame.
function FoldedText({
  head,
  rest,
  shown,
  onShow,
  className,
  file = false,
}: {
  head: string;
  rest: string;
  shown: boolean;
  onShow: () => void;
  className: string;
  /** A file's content, whose whitespace at a line's end is spelled too. */
  file?: boolean;
}) {
  const [restChars, restLines] = useMemo(() => [[...rest].length, rest.split('\n').length - 1], [rest]);
  let headTrailing: 'lines' | 'end' | undefined;
  let restTrailing: 'lines' | 'end' | undefined;
  if (file) {
    // The head ends a line where the fold falls on a newline, or where it is all.
    headTrailing = rest === '' || rest.startsWith('\n') ? 'end' : 'lines';
    restTrailing = 'end';
  }
  return (
    <>
      <pre className={className}>
        <VisibleText text={head} trailing={headTrailing} />
        {shown && <VisibleText text={rest} trailing={restTrailing} />}
      </pre>
      {rest !== '' && !shown && (
        <Button type="button" size="xs" variant="outline" className="mt-1" onClick={onShow}>
          Show the rest — {restChars} more characters{restLines > 0 && `, ${restLines} more lines`}
        </Button>
      )}
    </>
  );
}

// text as cutText folds it for a request, once per text: a write's body runs to
// 1 MiB and the transcript re-renders on every frame.
function useCut(text: string) {
  return useMemo(() => cutText(text, COMMAND_CHARS, COMMAND_LINES), [text]);
}

// A settled call's long text, folded as on its request: a write's content, an
// Agent call's brief, or a query's SQL.
function FoldedBlock({ text, className, file = false }: { text: string; className: string; file?: boolean }) {
  const [shown, setShown] = useState(false);
  const { head, rest } = useCut(text);
  return (
    <FoldedText head={head} rest={rest} shown={shown} onShow={() => setShown(true)} className={className} file={file} />
  );
}

type EditAction = NonNullable<NonNullable<ChatToolCall['action']>['edit']>;

// The strings an edit replaced, drawn as on its request.
function EditStrings({ edit }: { edit: EditAction }) {
  const [shown, setShown] = useState({ old: false, new: false });
  return (
    <EditBlocks
      edit={edit}
      shown={shown}
      onShow={(which) => setShown((prev) => ({ ...prev, [which]: true }))}
      className="mt-1 border-l-2 border-muted pl-3 font-mono break-all whitespace-pre-wrap"
    />
  );
}

type EditShown = { old: boolean; new: boolean };

// Whether either of an edit's strings is still folded.
function editFolded(edit: EditAction, shown: EditShown): boolean {
  const folded = (text: string) => cutText(text, COMMAND_CHARS, COMMAND_LINES).rest !== '';
  return (folded(edit.oldString) && !shown.old) || (folded(edit.newString) && !shown.new);
}

// An edit's two strings: the text that goes over the text that replaces it,
// each drawn as a file's content is, since each is a piece of one, and folded
// on its own.
function EditBlocks({
  edit,
  shown,
  onShow,
  className,
}: {
  edit: EditAction;
  shown: EditShown;
  onShow: (which: keyof EditShown) => void;
  className: string;
}) {
  const old = cutText(edit.oldString, COMMAND_CHARS, COMMAND_LINES);
  const replacement = cutText(edit.newString, COMMAND_CHARS, COMMAND_LINES);
  return (
    <>
      {edit.replaceAll && <p className="mt-1 text-xs text-muted-foreground">Every occurrence in the file.</p>}
      <p className="mt-1 text-xs text-muted-foreground">Replace</p>
      <FoldedText
        head={old.head}
        rest={old.rest}
        shown={shown.old}
        onShow={() => onShow('old')}
        className={className}
        file
      />
      <p className="mt-1 text-xs text-muted-foreground">{edgesOf(edit.oldString)}</p>
      <p className="mt-1 text-xs text-muted-foreground">With</p>
      {edit.newString === '' ? (
        <p className="mt-1 text-xs text-muted-foreground">The text will be deleted.</p>
      ) : (
        <>
          <FoldedText
            head={replacement.head}
            rest={replacement.rest}
            shown={shown.new}
            onShow={() => onShow('new')}
            className={className}
            file
          />
          <p className="mt-1 text-xs text-muted-foreground">{edgesOf(edit.newString)}</p>
        </>
      )}
      {edit.oldString.replace(/\s/g, '') === edit.newString.replace(/\s/g, '') && (
        <p className="mt-1 text-xs text-muted-foreground">The two differ only in whitespace.</p>
      )}
    </>
  );
}

// How a piece of a file meets what is around it: a newline at either end
// decides whether an edit joins or splits lines, and a <pre> draws neither.
// A CRLF file's strings can carry \r\n, whose \r would hide a leading \n.
function edgesOf(text: string): string {
  const starts = text.startsWith('\n') || text.startsWith('\r\n');
  const ends = text.endsWith('\n');
  if (starts && ends) return 'Starts and ends with a newline.';
  if (starts) return 'Starts with a newline.';
  if (ends) return 'Ends with a newline.';
  return 'No newline at either end.';
}

// The calls that are not on the user: one closed disclosure each, the summary
// with the model's description under it, and what the model read as its body,
// all text. The summary is spelled like the approval request, so an override in
// it cannot reorder the tag, and carries no title, since a native tooltip would
// draw it raw. A call its tool could not show reads as its tool's name.
function ToolCalls({
  calls,
  byAgent,
  modelLabel,
}: {
  calls: ChatToolCall[];
  byAgent: SubagentCalls;
  modelLabel: ModelLabel;
}) {
  return (
    <div className="mt-1 text-xs text-muted-foreground">
      {calls.map((call) => {
        const summary = summaryOf(call);
        const line = descriptionLine(call.action?.description ?? '');
        const cwd = call.action?.command?.cwd ?? '';
        const sandboxed = call.action?.command?.sandboxed ?? false;
        const written = writeOf(call.action);
        const edit = call.action?.edit ?? null;
        const delegate = call.action?.delegate ?? null;
        const tag = toolCallTag(call);
        // A write that ran unasked is one in the chat's workspace: what it wrote
        // is drawn open, since a later command may run it.
        const open = call.approval === null && call.status === 'Succeeded';
        const changed = (
          <>
            {written && (
              <FoldedBlock
                text={written.content}
                className="mt-1 border-l-2 border-muted pl-3 font-mono break-all whitespace-pre-wrap"
                file
              />
            )}
            {edit && <EditStrings edit={edit} />}
          </>
        );
        return (
          <Fragment key={call.id}>
            <details>
              <summary className="cursor-pointer select-none">
                <span className="font-mono break-all">
                  <VisibleText text={summary} />
                </span>
                {call.action?.memory?.scope === 'everywhere' && (
                  <span className="ml-2 opacity-70">for every cluster</span>
                )}
                {tag !== '' && <span className="ml-2 opacity-70">{tag}</span>}
                {line !== '' && <ModelDescription line={line} />}
              </summary>
              {cwd !== '' && <LabelledLine label="in" text={cwd} after={sandboxed ? ', sandboxed' : undefined} />}
              {!open && changed}
              {call.action?.kubeQuery && (
                <FoldedBlock
                  text={call.action.kubeQuery.sql}
                  className="mt-1 border-l-2 border-muted pl-3 font-mono break-all whitespace-pre-wrap"
                />
              )}
              {call.action?.memory?.op === 'save' && call.status === 'Succeeded' && (
                <MemoryKept body={call.action.memory.body} />
              )}
              {delegate ? (
                <AgentBody
                  delegate={delegate}
                  calls={byAgent.get(call.id) ?? NO_CALLS}
                  report={call.background?.report ?? ''}
                  modelLabel={modelLabel}
                />
              ) : (
                <pre className="mt-1 border-l-2 border-muted pl-3 font-mono break-all whitespace-pre-wrap">
                  {call.output}
                </pre>
              )}
              <ClusterWriteLines call={call} />
            </details>
            {open && changed}
            {call.background?.status === 'Running' && <StopTask id={call.id} />}
          </Fragment>
        );
      })}
    </div>
  );
}

// A call's cluster writes that no longer wait, one line each: the method and
// the path, tagged with what the user or the permissions engine decided, and
// for the engine's, the mode or rule that decided it. `approved` and `allowed`
// are the decision, not that the cluster received it; the output says what the
// command read back.
function ClusterWriteLines({ call }: { call: ChatToolCall }) {
  const settled = call.clusterWrites.filter((w) => !isWaitingWrite(call, w));
  if (settled.length === 0) return null;
  return (
    <ul className="mt-1 border-l-2 border-muted pl-3">
      {settled.map((w) => (
        <li key={w.approval.id}>
          <span className="font-mono break-all">
            <VisibleText text={`${w.method} ${w.path}`} />
          </span>
          {w.dryRun && ' (dry run)'}
          <span className="ml-2 opacity-70">{clusterWriteTag(w)}</span>
          {w.reason && (
            <span className="ml-2 text-muted-foreground">
              <VisibleText text={w.reason} />
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}

// What became of a write that no longer waits. A pending one here is one a
// crash stranded, as an abandoned one's wait ended with nobody's answer.
function clusterWriteTag(w: ChatClusterWrite): string {
  switch (w.approval.status) {
    case 'Approved':
      return 'approved';
    case 'Denied':
      return 'denied';
    case 'Allowed':
      return 'allowed';
    case 'Refused':
      return 'refused';
    default:
      return 'not answered';
  }
}

// A model id as the reader is shown it: the catalog's label, or the id itself.
type ModelLabel = (id: string) => string;

// A subagent's calls not awaiting approval, by the Agent call they ran under.
type SubagentCalls = ReadonlyMap<string, ChatToolCall[]>;

const NO_CALLS: ChatToolCall[] = [];
const NO_SUBAGENT_CALLS: SubagentCalls = new Map();

// An answer's calls split into its own and its subagents', which are drawn
// inside their Agent call. A subagent's call awaiting approval is in neither: it
// is the request.
function splitCalls(calls: ChatToolCall[]): { own: ChatToolCall[]; byAgent: SubagentCalls } {
  const own: ChatToolCall[] = [];
  const byAgent = new Map<string, ChatToolCall[]>();
  calls.forEach((call) => {
    if (call.agentCallID === null) {
      own.push(call);
    } else if (call.status !== 'AwaitingApproval') {
      byAgent.set(call.agentCallID, [...(byAgent.get(call.agentCallID) ?? []), call]);
    }
  });
  return { own, byAgent };
}

type DelegateAction = NonNullable<NonNullable<ChatToolCall['action']>['delegate']>;

// What an Agent call handed on and got back: the kind of agent and, when the
// call named one, the model it ran on; the brief, folded like a command; the
// subagent's calls, drawn as the answer's own are; and its report, off the task
// once the agent completed. The brief and the report are the models' text, drawn
// as text and markdown.
function AgentBody({
  delegate,
  calls,
  report,
  modelLabel,
}: {
  delegate: DelegateAction;
  calls: ChatToolCall[];
  report: string;
  modelLabel: ModelLabel;
}) {
  const kind = delegate.model === '' ? delegate.agentType : `${delegate.agentType} · ${modelLabel(delegate.model)}`;
  return (
    <>
      <p className="mt-1">
        <VisibleText text={kind} />
      </p>
      <FoldedBlock text={delegate.prompt} className="mt-1 border-l-2 border-muted pl-3 break-all whitespace-pre-wrap" />
      {calls.length > 0 && <ToolCalls calls={calls} byAgent={NO_SUBAGENT_CALLS} modelLabel={modelLabel} />}
      {report !== '' && (
        <div className="mt-1 border-l-2 border-muted pl-3 text-foreground">
          <Markdown text={report} />
        </div>
      )}
    </>
  );
}

// A value the sidecar bounds, as one line after a muted label: where a command
// runs, or the host a fetch dials. Never folded or cut, and spelled like the
// command, since the model chose it.
// A muted label, then text in mono through VisibleText, then an optional muted
// note that is the app's own, outside the text.
function LabelledLine({ label, text, after }: { label: string; text: string; after?: string }) {
  return (
    <p className="mt-1 flex min-w-0 gap-1 text-xs">
      <span className="shrink-0 text-muted-foreground">{label}</span>
      <span className="min-w-0 font-mono break-all">
        <VisibleText text={text} />
      </span>
      {after && <span className="-ml-1 shrink-0 text-muted-foreground">{after}</span>}
    </p>
  );
}

// The model's description of a command, as descriptionLine reads it: italic and
// quoted, in the color of wherever it sits, so it reads as the model's claim and
// never outshines the command. One line, cut by an ellipsis inside the quotes;
// no title, since a native tooltip draws the text unspelled.
function ModelDescription({ line }: { line: string }) {
  return (
    <span className="flex min-w-0 italic">
      <span>“</span>
      <span className="min-w-0 truncate">
        <VisibleText text={line} />
      </span>
      <span>”</span>
    </span>
  );
}

type CommandAction = NonNullable<NonNullable<ChatToolCall['action']>['command']>;

// What a command's request asks. On a machine with a sandbox a sandboxed command
// runs unasked, so every command that asks runs outside it, whichever way the
// chat is switched now: the turn read the switch when it started. Unknown reads
// as outside, which a machine with no sandbox is too.
function commandHeading(command: CommandAction, sandboxAvailable: boolean | undefined): string {
  if (sandboxAvailable !== false) {
    return command.background
      ? 'Run this command in the background, outside the sandbox?'
      : 'Run this command outside the sandbox?';
  }
  return command.background ? 'Run this command in the background?' : 'Run this command?';
}

// The call a turn is stopped on, and the only place it is approved: a command,
// a file read by its path, a file written by its path and content, a file
// edited by its path and both strings, or a page fetched by its URL. It shows
// the id the row carries and sends it back with one boolean; the message
// changing is what takes the request down. The command and the content wrap
// anywhere and nothing caps their height, so the head reaches the eye whole
// however narrow the panel.
// `live` is a pending approval on a turn still waiting: anything else is a
// choice that no longer exists. A request with no kind to draw offers no
// Approve: what the user cannot see, they cannot approve. `agent` is the
// description of the Agent call a subagent's request ran under, null on the
// answer's own: the user is approving a call whose reasoning they cannot see,
// so the request says whose it is. `change` is a cluster write the call's
// sandboxed command sent, which is what the request asks about when it is set;
// the action is its heading, and the call's command is drawn under it as what
// sent it.
function ApprovalRequest({
  approval,
  action,
  change,
  live,
  agent,
  armMs,
  sandboxAvailable,
}: {
  approval: NonNullable<ChatToolCall['approval']>;
  action: ChatToolCall['action'];
  change: ChatClusterWrite | null;
  live: boolean;
  agent: string | null;
  armMs: number;
  sandboxAvailable: boolean | undefined;
}) {
  const [, approvalDecide] = useMutation(ApprovalDecideMutation);
  const buttons = useRef<HTMLDivElement>(null);
  const armed = useHeldStill(buttons, armMs);
  const [decided, setDecided] = useState(false);
  const [failed, setFailed] = useState(false);
  const [shown, setShown] = useState(false);
  const [diffShown, setDiffShown] = useState(false);
  const [editShown, setEditShown] = useState({ old: false, new: false });
  const command = action?.command ?? null;
  const read = action?.read ?? null;
  const write = writeOf(action);
  const edit = action?.edit ?? null;
  const page = action?.fetch ?? null;
  // A memory call for one cluster never asks, so it is no kind this draws.
  const memory = action?.memory?.scope === 'everywhere' ? action.memory : null;
  const folding = change ? change.body : (command?.text ?? write?.content ?? memory?.body ?? '');
  const { head, rest } = useCut(folding);
  const diff = useCut(change?.diff ?? '');
  const line = descriptionLine(action?.description ?? '');
  // A whole diff is what a change's user reads, so the request behind it is a
  // fold Approve does not wait on; with no diff, or one cut short, the request
  // is drawn open and Approve waits on its body.
  const requestOpen = change === null || change.diff === '' || change.diffCut;
  const folded =
    (rest !== '' && !shown && requestOpen) ||
    (diff.rest !== '' && !diffShown) ||
    (edit !== null && editFolded(edit, editShown));

  // Only an error hands the buttons back. A false answer means no turn was
  // waiting — a cancel or a settle is on its way through the watch — so they
  // stay down.
  const decide = async (approve: boolean) => {
    setDecided(true);
    setFailed(false);
    const result = await approvalDecide({ id: approval.id, approve });
    if (result.error) {
      setDecided(false);
      setFailed(true);
    }
  };

  let label = 'Command awaiting approval';
  // Only a request drawn below offers Approve: what the user cannot see is not
  // approved.
  let drawn = true;
  let body: ReactNode;
  if (change && change.method === '') {
    // An action with no request of its own is a kind a later step draws.
    label = 'Cluster change awaiting approval';
    drawn = false;
    body = <p className="text-xs text-muted-foreground">This request can&apos;t be shown.</p>;
  } else if (change) {
    // The action is the heading, the sidecar's summary, so nothing here parses
    // a path; the diff a dry run computed is what changes. The request itself
    // is never a reading of it: the path and query as sent, one line the proxy
    // bounds; the method, and the media type, which decides what a patch's body
    // does; then the body as a file's content is. The command under Sent by is
    // context, so Approve does not wait on its fold.
    label = 'Cluster change awaiting approval';
    const sent = (
      <>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={change.path} />
        </p>
        <p aria-label="Method and media type" className="mt-1 font-mono text-xs break-all">
          {change.method}
          {change.contentType !== '' && (
            <>
              {' '}
              <VisibleText text={change.contentType} />
            </>
          )}
        </p>
        {change.body !== '' && (
          <FoldedText
            head={head}
            rest={rest}
            shown={shown}
            onShow={() => setShown(true)}
            className={REQUEST_TEXT}
            file
          />
        )}
      </>
    );
    body = (
      <>
        <p className="text-xs text-muted-foreground">
          <VisibleText text={change.action.summary} />
          {change.dryRun && ' (dry run)'}
        </p>
        {change.diff !== '' && (
          <DiffBlock head={diff.head} rest={diff.rest} shown={diffShown} onShow={() => setDiffShown(true)} />
        )}
        {change.diffError !== '' && (
          <p className="mt-1 text-xs text-muted-foreground">
            No preview: <VisibleText text={change.diffError} />
          </p>
        )}
        {requestOpen ? (
          sent
        ) : (
          <details className="mt-1">
            <summary className="cursor-pointer text-xs text-muted-foreground">Show the request</summary>
            {sent}
          </details>
        )}
        {command && (
          <>
            <p className="mt-1 text-xs text-muted-foreground">Sent by</p>
            <FoldedBlock text={command.text} className={REQUEST_TEXT} />
          </>
        )}
      </>
    );
  } else if (command) {
    body = (
      <>
        <p className="text-xs text-muted-foreground">{commandHeading(command, sandboxAvailable)}</p>
        {command.background && (
          <p className="text-xs text-muted-foreground">
            It keeps running after this answer, until it exits or you stop it.
          </p>
        )}
        {line !== '' && (
          <p className="mt-1 flex min-w-0 gap-1 text-xs">
            <span className="shrink-0 text-muted-foreground">The model says:</span>
            <ModelDescription line={line} />
          </p>
        )}
        <FoldedText head={head} rest={rest} shown={shown} onShow={() => setShown(true)} className={REQUEST_TEXT} />
        {command.cwd !== '' && <LabelledLine label="in" text={command.cwd} />}
      </>
    );
  } else if (write) {
    // The path is one line, as a read's is; the content is every byte the file
    // will hold, so each \r, each space or tab ending a line, and whether the
    // last line ends with a newline are drawn, none of which the eye sees in a
    // <pre>.
    label = 'File write awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Write this file?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={write.path} />
        </p>
        {write.content === '' ? (
          <p className="mt-1 text-xs text-muted-foreground">The file will be empty.</p>
        ) : (
          <>
            <FoldedText
              head={head}
              rest={rest}
              shown={shown}
              onShow={() => setShown(true)}
              className={REQUEST_TEXT}
              file
            />
            <p className="mt-1 text-xs text-muted-foreground">
              {write.content.endsWith('\n') ? 'Ends with a newline.' : 'No newline at the end.'}
            </p>
          </>
        )}
      </>
    );
  } else if (edit) {
    // The path is drawn as a write's; the two strings are pieces of the file,
    // each folded on its own, and Approve waits on both.
    label = 'File edit awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Edit this file?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={edit.path} />
        </p>
        <EditBlocks
          edit={edit}
          shown={editShown}
          onShow={(which) => setEditShown((prev) => ({ ...prev, [which]: true }))}
          className={REQUEST_TEXT}
        />
      </>
    );
  } else if (read) {
    // fileguard.Abs refuses a path with a control character or past 4,096 bytes,
    // so it is one line and never folded.
    label = 'File read awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Read this file?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={read.path} />
        </p>
      </>
    );
  } else if (page) {
    // The sidecar caps the URL at 8,192 bytes, so it is never folded. The host
    // is the one it dials, on a line of its own, since a long URL can hide it.
    label = 'Page fetch awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Fetch this page?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={page.url} />
        </p>
        <LabelledLine label="Host:" text={page.host} />
      </>
    );
  } else if (memory?.op === 'save') {
    // The name has passed the store's check before any request exists, so it is
    // one short line. The body is kept as sent, so its line ends are spelled as a
    // file's are.
    label = 'Memory save awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Remember this for every cluster?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={memory.name} />
        </p>
        <FoldedText head={head} rest={rest} shown={shown} onShow={() => setShown(true)} className={REQUEST_TEXT} file />
        <p className="mt-1 text-xs text-muted-foreground">Kstack will read it in every cluster&apos;s chats.</p>
      </>
    );
  } else if (memory?.op === 'forget') {
    // A forget carries no body, and the dialog shows what the note holds.
    label = 'Memory forget awaiting approval';
    body = (
      <>
        <p className="text-xs text-muted-foreground">Forget this memory for every cluster?</p>
        <p className="mt-1 font-mono text-xs break-all whitespace-pre-wrap">
          <VisibleText text={memory.name} />
        </p>
      </>
    );
  } else {
    drawn = false;
    body = <p className="text-xs text-muted-foreground">This request can&apos;t be shown.</p>;
  }

  return (
    <div
      id={approvalAnchor(approval.id)}
      role="group"
      aria-label={label}
      className="mt-2 rounded-md border border-border p-2"
    >
      {agent !== null && (
        <p className="mb-1 flex min-w-0 gap-1 text-xs text-muted-foreground">
          <span className="shrink-0">An agent asks:</span>
          {agent !== '' && <ModelDescription line={agent} />}
        </p>
      )}
      {body}
      {failed && <p className="mt-1 text-xs text-destructive">The decision did not reach the sidecar. Try again.</p>}
      {/* Approve waits for its place to hold; Deny never does, since refusing the
          wrong request costs one more question, not a command. */}
      <div ref={buttons} className="mt-2 flex gap-2">
        {drawn && (
          <Button type="button" size="xs" disabled={!live || decided || folded || !armed} onClick={() => decide(true)}>
            Approve
          </Button>
        )}
        <Button type="button" size="xs" variant="outline" disabled={!live || decided} onClick={() => decide(false)}>
          Deny
        </Button>
      </div>
    </div>
  );
}

// How a notice's task ended, in the transcript's words, and for a command
// whether an agent started it: the answer that asked for it never drew the call.
function noticeHead(notice: TaskNotice): string {
  if (notice.kind === 'agent') return agentNoticeEnd(notice);
  return noticeEnd(notice) + (notice.agentDescription !== '' ? ' · started by an agent' : '');
}

// An agent's end. Its report is not drawn here: the user reads it in the Agent call.
function agentNoticeEnd(notice: TaskNotice): string {
  switch (notice.status) {
    case 'completed':
      return 'Agent finished';
    case 'failed':
      return 'Agent failed';
    case 'stopped':
      return notice.stoppedBy === 'unanswered' ? 'Agent stopped: no answer in 30 minutes' : 'Agent stopped';
    default:
      return 'Agent lost when Kstack stopped';
  }
}

function noticeEnd(notice: TaskNotice): string {
  switch (notice.status) {
    case 'exited':
      return notice.exitCode === null
        ? 'Background command finished'
        : `Background command finished · exit ${notice.exitCode}`;
    case 'stopped':
      return 'Background command stopped';
    default:
      return 'Background command lost when Kstack stopped';
  }
}

// One muted line per notice a message carried: how the task ended, then the task
// by the model's description, else a command's text. Both are the model's, so
// they are drawn as the transcript's calls are.
function Notices({ notices }: { notices: TaskNotice[] }) {
  return (
    <div className="flex w-full flex-col items-end gap-1 text-xs text-muted-foreground">
      {notices.map((notice) => (
        <p key={notice.id} className="flex max-w-[80%] min-w-0 gap-1">
          <span className="shrink-0">{noticeHead(notice)} —</span>
          {notice.description !== '' ? (
            <ModelDescription line={notice.description} />
          ) : (
            <span className="min-w-0 truncate font-mono">
              <VisibleText text={notice.command} />
            </span>
          )}
        </p>
      ))}
    </div>
  );
}

function Message({
  message,
  label,
  labelOf,
  onAskAgain,
  approveArmMs,
  sandboxAvailable,
}: {
  message: ChatMessage;
  approveArmMs: number;
  sandboxAvailable: boolean | undefined;
  /** The model this answer ran on, when it differs from the answer before it. */
  label?: string;
  /** How a model is named, by its provider and id. */
  labelOf: (providerID: string | undefined, id: string) => string;
  /** Present on a failed last answer alone; disabled while anything else is in flight. */
  onAskAgain?: () => void;
}) {
  const text = textOf(message.content);
  const card = contextOf(message.content);
  const sources = sourcesOf(message.citations);
  const searches = searchesOf(message.toolCalls);
  const notices = noticesOf(message.content);
  const { own, byAgent } = splitCalls(message.toolCalls);
  // A search that ran is drawn by its query alone, under Searched the web; every
  // other call is a disclosure, the provider's included, since it left the machine.
  const done = own.filter((call) => !ranSearch(call) && call.status !== 'AwaitingApproval');
  const waiting = waitingRequestsOf(message.toolCalls);
  const agentOf = (call: ChatToolCall) =>
    call.agentCallID === null
      ? null
      : descriptionLine(message.toolCalls.find((c) => c.id === call.agentCallID)?.action?.description ?? '');
  const modelLabel = (id: string) => labelOf(message.provider?.id, id);
  const mine = message.role === 'User';
  // A message of notices alone is its lines and nothing else: there is no bubble.
  // Its context, when the sandbox switch moved, is drawn above them.
  if (mine && text === '' && notices.length > 0) {
    return (
      <article>
        {card !== '' && <ClusterContext card={card} />}
        <Notices notices={notices} />
      </article>
    );
  }
  return (
    <article className={mine ? 'flex flex-col items-end gap-1' : 'flex flex-col items-start gap-1'}>
      {mine && notices.length > 0 && <Notices notices={notices} />}
      {!mine && (
        <div className="px-1">
          <AppLogo />
        </div>
      )}
      {label && <p className="px-1 text-xs text-muted-foreground">{label}</p>}
      <div
        className={
          mine
            ? 'max-w-[80%] rounded-lg bg-muted px-3 py-2 text-sm whitespace-pre-wrap'
            : 'max-w-[80%] px-1 py-2 text-sm'
        }
      >
        {mine && card !== '' && <ClusterContext card={card} />}
        {!mine && message.thinking !== '' && (
          <Thinking summary={message.thinking} stillThinking={inFlight(message.status) && text === ''} />
        )}
        {mine ? text : <Markdown text={text} />}
        {inFlight(message.status) && (
          <span className="ml-1 inline-flex align-middle" role="status" aria-label="Answering">
            <Spinner size="sm" />
          </span>
        )}
        {done.length > 0 && <ToolCalls calls={done} byAgent={byAgent} modelLabel={modelLabel} />}
        {waiting.map(({ call, approval, change }) => (
          // Keyed on the approval, never the call or the message, so a decided
          // request's pressed state never reaches the next. Live while a run of
          // the answer waits on the user, which an agent's can after it settled.
          <ApprovalRequest
            key={approval.id}
            approval={approval}
            action={call.action}
            change={change}
            live={approval.status === 'Pending' && message.awaitingApproval}
            agent={agentOf(call)}
            armMs={approveArmMs}
            sandboxAvailable={sandboxAvailable}
          />
        ))}
        {sources.length > 0 && <Sources sources={sources} />}
        {searches.count > 0 && <Searches count={searches.count} queries={searches.queries} />}
        {message.status === 'Cancelled' && <p className="mt-1 text-xs text-muted-foreground">Stopped</p>}
        {message.status === 'Complete' &&
          (text === '' || message.finishReason === 'tool_use' || message.finishReason === 'pause_turn') && (
            <p className="text-xs text-muted-foreground">Stopped: {message.finishReason}</p>
          )}
        {message.status === 'Failed' && (
          <p className="mt-1 text-xs text-destructive">{message.error || 'The answer failed.'}</p>
        )}
      </div>
      {onAskAgain && (
        <Button type="button" variant="outline" size="xs" onClick={onAskAgain}>
          Ask again
        </Button>
      )}
    </article>
  );
}

// The question a failed answer followed, which Ask again sends afresh; null when
// that message was notices alone, since then there is nothing to ask.
function questionBefore(messages: ChatMessage[], index: number): string | null {
  const question = messages
    .slice(0, index)
    .reverse()
    .find((m) => m.role === 'User');
  if (!question) return '';
  const text = textOf(question.content);
  if (text === '' && noticesOf(question.content).length > 0) return null;
  return text;
}

export function ChatTranscript({
  messages,
  phase,
  chatID,
  mode,
  clusterID,
  approveArmMs = APPROVE_ARM_MS,
  sandboxAvailable,
  sandboxDisabled,
  switching = false,
}: ChatTranscriptProps) {
  const { models } = useModels();
  const { send, askAgain } = useChatOutbox(mode, chatID, clusterID);
  const scroller = useRef<HTMLDivElement>(null);
  // Whether the reader is at the end, sampled as they scroll. Starts true so opening
  // a chat lands at the bottom.
  const following = useRef(true);

  // After every commit, follow the answer down — but only for a reader who was
  // already there.
  useEffect(() => {
    const el = scroller.current;
    if (el && following.current) el.scrollTop = el.scrollHeight;
  });

  const onScroll = () => {
    const el = scroller.current;
    if (el) following.current = el.scrollHeight - el.scrollTop - el.clientHeight <= PIN_SLACK_PX;
  };

  // A model's name: the catalog's display name, or the stored id where a key has
  // since been taken away.
  const labelOf = useCallback(
    (providerID: string | undefined, id: string) =>
      modelOf(models, providerID ? { providerID, id } : null)?.label ?? id,
    [models],
  );

  const drawn = messages.filter(isDrawn);
  // Labelled where the model changed, so a switch is visible where it happened and a
  // chat that stayed on one model — the first answer included — stays unlabelled.
  let ran: string | undefined;
  const rows = drawn.map((message) => {
    const answered = message.role === 'Assistant' && message.model !== '';
    const label =
      answered && ran !== undefined && message.model !== ran ? labelOf(message.provider?.id, message.model) : undefined;
    if (answered) ran = message.model;
    return { message, label };
  });

  // Only the last row: a fresh send lands at the end, and an answer to a question
  // mid-transcript would sit far from it. Nothing is asked twice while the entry's
  // one send is busy, which a second turn into the chat would be refused for anyway.
  const last = drawn.at(-1);
  const failed = last?.role === 'Assistant' && last.status === 'Failed' && last.provider !== null;
  const question = failed ? questionBefore(messages, messages.indexOf(last)) : null;
  const onAskAgain =
    failed && question !== null && send.status === 'idle' && !switching && sandboxDisabled !== undefined
      ? () =>
          askAgain(
            question,
            { model: { providerID: last.provider!.id, id: last.model }, effort: last.effort },
            sandboxDisabled,
          )
      : undefined;

  let body;
  if (phase === 'connecting') {
    body = (
      <div className="flex items-center gap-2 text-sm text-muted-foreground">
        <Spinner size="sm" />
        Loading messages…
      </div>
    );
  } else if (drawn.length === 0) {
    body = <p className="text-sm text-muted-foreground">No messages yet.</p>;
  } else {
    body = rows.map(({ message, label }) => (
      <Message
        key={message.id}
        message={message}
        label={label}
        labelOf={labelOf}
        onAskAgain={message.id === last?.id ? onAskAgain : undefined}
        approveArmMs={approveArmMs}
        sandboxAvailable={sandboxAvailable}
      />
    ));
  }

  return (
    <div
      ref={scroller}
      onScroll={onScroll}
      data-testid="chat-transcript"
      className="flex flex-1 flex-col gap-3 overflow-y-auto p-4"
    >
      {body}
    </div>
  );
}
