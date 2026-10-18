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
// A LogsRead call in the transcript: one block naming what the model read and over
// which range, the model's description, and the lines it read as the action holds
// them, stripped, redacted and cut. Nothing is fetched, so a reload, an evicted log
// or a gone cluster draws the same lines. A footer says what the model saw beyond
// them. View around this makes the read's view the focused view, as a LogsView
// card's Expand does, in chat mode alone.
import { useState } from 'react';

import { Button } from '@kubetail/ui/elements/button';

import { GapRow, LineRow, readsOneStream } from '@/components/widgets/logs-view-inline';
import { SourceLine } from '@/components/widgets/logs-view-lines';
import { ModelDescription } from '@/components/widgets/model-description';
import { VisibleText } from '@/components/widgets/visible-text';
import type { AppMode } from '@/lib/app-mode';
import type { ChatToolCall } from '@/lib/chats';
import { INLINE_LINES } from '@/lib/inline-log-lines';
import type { LogLine } from '@/lib/log-viewer';
import type { LogsReadAction } from '@/lib/logs-view';
import { useLogsView } from '@/lib/logs-view';
import { useRightSidebar } from '@/lib/right-sidebar';
import { descriptionLine } from '@/lib/visible-text';

type Line = LogsReadAction['lines'][number];

const NO_PLACE = { node: '', region: '', zone: '', os: '', arch: '' };

// A sample's line as the viewer's rows draw one, a watch's too. Lines are keyed by
// index: a sample holds no cursor.
export function logLine(line: Line, i: number): LogLine {
  const { timestamp, namespace, pod, container, previous, message, gap } = line;
  return {
    timestamp,
    cursor: String(i),
    message,
    stream: { namespace, pod, container, previous },
    place: NO_PLACE,
    gap,
  };
}

// The range as the model's header names it, and which end of it was read.
function rangeLine({ since, until, mode }: LogsReadAction): string {
  const ends = [since && `from ${since}`, until && `up to ${until}`].filter(Boolean);
  return [...ends, mode === 'Head' ? 'oldest first' : 'the newest'].join(', ');
}

const OLDER = { End: '', More: 'older lines exist', Unread: 'older lines may exist' };
const NEWER = { End: '', More: 'newer lines exist', Unread: 'newer lines may exist' };

// What the model saw beyond the lines, in the header's words.
function Beyond({ dropped, before, after, skipped }: LogsReadAction) {
  const words = [
    dropped > 0 && `${dropped} more ${dropped === 1 ? 'line was' : 'lines were'} left out`,
    OLDER[before],
    NEWER[after],
  ].filter(Boolean);
  if (words.length === 0 && skipped.length === 0) return null;
  return (
    <p data-testid="logs-read-beyond" className="mt-1 opacity-70">
      {words.join('; ')}
      {skipped.map((line, i) => (
        <span key={line}>
          {(words.length > 0 || i > 0) && '; '}
          could not read <VisibleText text={line} />
        </span>
      ))}
    </p>
  );
}

export function LogsReadCard({
  call,
  action,
  chatID,
  clusterID,
  mode,
}: {
  call: ChatToolCall;
  action: LogsReadAction;
  chatID: string;
  /** The chat's cluster. Undefined until the list watch delivers the chat. */
  clusterID: string | undefined;
  mode: AppMode;
}) {
  const { view, set } = useLogsView();
  const { open } = useRightSidebar();
  const [all, setAll] = useState(false);
  const [first] = action.sources;
  const more = action.sources.length - 1;
  const line = descriptionLine(call.action?.description ?? '');
  const shown = mode === 'chat' && open && view?.chatId === chatID && view.callId === call.id;
  const oneStream = readsOneStream(action);
  const lines = all ? action.lines : action.lines.slice(0, INLINE_LINES);
  const { viewAround } = action;
  return (
    <div className="mt-1 rounded-md border px-3 py-2">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="font-semibold text-foreground">
            Read: <SourceLine source={first} />
            {more > 0 && ` and ${more} more`}
          </p>
          <p>
            {rangeLine(action)}
            {action.grep !== '' && (
              <>
                {', matching '}
                <span className="font-mono">
                  /<VisibleText text={action.grep} />/
                </span>
              </>
            )}
          </p>
          {line !== '' && <ModelDescription line={line} />}
        </div>
        {mode === 'chat' &&
          viewAround &&
          (shown ? (
            <span className="shrink-0 opacity-70">Showing in the sidebar</span>
          ) : (
            <Button
              variant="outline"
              size="sm"
              className="shrink-0"
              disabled={clusterID === undefined}
              onClick={() => {
                if (clusterID !== undefined)
                  set({ clusterId: clusterID, chatId: chatID, callId: call.id, action: viewAround });
              }}
            >
              View around this
            </Button>
          ))}
      </div>
      <div
        data-testid="logs-read"
        className="mt-2 overflow-hidden rounded border bg-background font-mono leading-6 text-foreground"
      >
        {lines.length === 0 && <p className="px-2 text-muted-foreground">No lines</p>}
        {lines.map((read, i) => {
          const row = logLine(read, i);
          return (
            <div key={row.cursor}>
              {row.gap && <GapRow line={row} />}
              <LineRow line={row} oneStream={oneStream} />
            </div>
          );
        })}
      </div>
      {!all && action.lines.length > INLINE_LINES && (
        <Button variant="ghost" size="sm" className="mt-1" onClick={() => setAll(true)}>
          Show all {action.lines.length} lines
        </Button>
      )}
      <Beyond {...action} />
    </div>
  );
}
