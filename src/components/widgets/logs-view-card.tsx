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
// A LogsView call in the transcript: one block naming what the view reads and
// where it opened, the model's description, the receipt it read, Expand, which
// makes this call the focused view, and the view's lines in a fixed window. While
// the sidebar shows the call, the card says so in Expand's place and draws no
// window. On the dashboard, which has no panel for a view yet, there is no Expand
// at all. Expand and the window wait for the chat's cluster.
import { useState } from 'react';
import { useClient } from 'urql';

import { Button } from '@kubetail/ui/elements/button';

import { LogsViewInline } from '@/components/widgets/logs-view-inline';
import { anchorLine, SourceLine } from '@/components/widgets/logs-view-lines';
import { ModelDescription } from '@/components/widgets/model-description';
import { VisibleText } from '@/components/widgets/visible-text';
import type { AppMode } from '@/lib/app-mode';
import type { ChatToolCall } from '@/lib/chats';
import { INLINE_LOADING, useInlineLogLines } from '@/lib/inline-log-lines';
import { createLogsClient } from '@/lib/logs-client';
import type { LogsViewAction } from '@/lib/logs-view';
import { useLogsView } from '@/lib/logs-view';
import { useRightSidebar } from '@/lib/right-sidebar';
import { descriptionLine } from '@/lib/visible-text';

// The window over the chat's cluster, its client built once per mount.
function InlineWindow({
  clusterID,
  action,
  until,
}: {
  clusterID: string;
  action: LogsViewAction;
  until: string | null;
}) {
  const urql = useClient();
  const [client] = useState(() => createLogsClient(urql, clusterID, action));
  const state = useInlineLogLines(client, action, until);
  return <LogsViewInline action={action} state={state} />;
}

export function LogsViewCard({
  call,
  action,
  chatID,
  clusterID,
  mode,
  until,
}: {
  call: ChatToolCall;
  action: LogsViewAction;
  chatID: string;
  /** The chat's cluster. Undefined until the list watch delivers the chat. */
  clusterID: string | undefined;
  mode: AppMode;
  /** The next view's `resolvedAt`, where this one stopped being live. Null while this is the chat's newest view, which alone may run live. */
  until: string | null;
}) {
  const { view, set } = useLogsView();
  const { open } = useRightSidebar();
  const [first] = action.sources;
  const more = action.sources.length - 1;
  const line = descriptionLine(call.action?.description ?? '');
  const shown = mode === 'chat' && open && view?.chatId === chatID && view.callId === call.id;
  return (
    <div className="mt-1 rounded-md border px-3 py-2">
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="font-semibold text-foreground">
            Logs: <SourceLine source={first} />
            {more > 0 && ` and ${more} more`}
          </p>
          <p>
            {anchorLine(action.anchor)}
            {action.pinToEnd && ', pinned to the end'}
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
          {call.output !== '' && (
            <p className="break-words opacity-70">
              <VisibleText text={call.output} />
            </p>
          )}
        </div>
        {mode === 'chat' &&
          (shown ? (
            <span className="shrink-0 opacity-70">Showing in the sidebar</span>
          ) : (
            <Button
              variant="outline"
              size="sm"
              className="shrink-0"
              disabled={clusterID === undefined}
              onClick={() => {
                if (clusterID !== undefined) set({ clusterId: clusterID, chatId: chatID, callId: call.id, action });
              }}
            >
              Expand
            </Button>
          ))}
      </div>
      {!shown &&
        (clusterID === undefined ? (
          <LogsViewInline action={action} state={INLINE_LOADING} />
        ) : (
          <InlineWindow clusterID={clusterID} action={action} until={until} />
        ))}
    </div>
  );
}
