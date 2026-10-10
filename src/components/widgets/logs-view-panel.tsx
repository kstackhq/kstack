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
// The log view the right sidebar draws in chat mode: a header naming what the
// focused view reads, a close that clears it, and the viewer under them. The panel
// does not scroll; the viewer owns its scroller.
import { X } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';

import { LogViewer } from '@/components/widgets/log-viewer';
import { anchorLine, Names, SourceLine } from '@/components/widgets/logs-view-lines';
import { VisibleText } from '@/components/widgets/visible-text';
import type { LogsView } from '@/lib/logs-view';
import type { ViewerStore } from '@/lib/log-viewer-store';

function Header({ view, onClose }: { view: LogsView; onClose: () => void }) {
  const { sources, filters, grep, anchor, pinToEnd } = view.action;
  const [first] = sources;
  const more = sources.length - 1;
  const detail = 'truncate text-xs text-muted-foreground';
  return (
    // The layout pins the sidebar toggle over the panel's top-right corner (16px in,
    // 28px square), so the header leaves that corner alone: the close sits level with
    // the toggle and 12px to its left.
    <header className="flex items-start gap-2 border-b p-4 pr-14">
      <div className="min-w-0 flex-1 text-sm">
        <h2 className="truncate font-semibold">
          Logs: <SourceLine source={first} />
          {more > 0 && ` and ${more} more`}
        </h2>
        <ul className="text-xs text-muted-foreground">
          {sources.map((source) => (
            <li key={`${source.namespace}/${source.kind}/${source.name}`} className="truncate">
              <SourceLine source={source} />
            </li>
          ))}
        </ul>
        {filters.map(({ field, values }) => (
          <p key={field} className={detail}>
            {field.toLowerCase()}: <Names names={values} />
          </p>
        ))}
        <p className={detail}>
          {anchorLine(anchor)}
          {pinToEnd && ', pinned to the end'}
        </p>
        {grep !== '' && (
          <p className={detail}>
            matching{' '}
            <span className="font-mono">
              /<VisibleText text={grep} />/
            </span>
          </p>
        )}
      </div>
      <Button variant="ghost" size="icon-sm" aria-label="Close log view" onClick={onClose}>
        <X aria-hidden />
      </Button>
    </header>
  );
}

export function LogsViewPanel({ view, viewer, onClose }: { view: LogsView; viewer: ViewerStore; onClose: () => void }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <Header view={view} onClose={onClose} />
      {/* Keyed on the call: a new call lands at its anchor, a re-render of the same
          call leaves the user where they scrolled. */}
      <LogViewer
        key={view.callId}
        query={view.action}
        openAt={view.action.anchor}
        pinToEnd={view.action.pinToEnd}
        store={viewer}
      />
    </div>
  );
}
