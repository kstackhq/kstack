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
// where it opened, the model's description, the receipt it read, and Expand,
// which makes this call the focused view. The focused call's card says so in
// Expand's place, and on the dashboard, which has no panel for a view yet, there
// is no Expand at all.
import { Button } from '@kubetail/ui/elements/button';

import { anchorLine, SourceLine } from '@/components/widgets/logs-view-lines';
import { ModelDescription } from '@/components/widgets/model-description';
import { VisibleText } from '@/components/widgets/visible-text';
import type { AppMode } from '@/lib/app-mode';
import type { ChatToolCall } from '@/lib/chats';
import type { LogsViewAction } from '@/lib/logs-view';
import { useLogsView } from '@/lib/logs-view';
import { descriptionLine } from '@/lib/visible-text';

export function LogsViewCard({
  call,
  action,
  chatID,
  mode,
}: {
  call: ChatToolCall;
  action: LogsViewAction;
  chatID: string;
  mode: AppMode;
}) {
  const { view, set } = useLogsView();
  const [first] = action.sources;
  const more = action.sources.length - 1;
  const line = descriptionLine(call.action?.description ?? '');
  const focused = view?.chatId === chatID && view.callId === call.id;
  return (
    <div className="mt-1 flex items-start gap-2 rounded-md border px-3 py-2">
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
        (focused ? (
          <span className="shrink-0 opacity-70">Showing in the sidebar</span>
        ) : (
          <Button
            variant="outline"
            size="sm"
            className="shrink-0"
            onClick={() => set({ chatId: chatID, callId: call.id, action })}
          >
            Expand
          </Button>
        ))}
    </div>
  );
}
