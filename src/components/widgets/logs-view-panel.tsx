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
import { VisibleText } from '@/components/widgets/visible-text';
import type { LogsView, LogsViewAction } from '@/lib/logs-view';
import type { ViewerStore } from '@/lib/log-viewer-store';

type Source = LogsViewAction['sources'][number];
type Anchor = LogsViewAction['anchor'];

// Names from the cluster or the model, comma-separated, each spelled through
// VisibleText.
function Names({ names }: { names: string[] }) {
  return names.map((name, i) => (
    // eslint-disable-next-line react/no-array-index-key
    <span key={i}>
      {i > 0 && ', '}
      <VisibleText text={name} />
    </span>
  ));
}

// `Deployment webapp in prod (app, istio-proxy), previous instance`: the enum's
// word is the app's own, the rest the cluster's.
function SourceLine({ source }: { source: Source }) {
  return (
    <>
      {source.kind} <VisibleText text={source.name} /> in <VisibleText text={source.namespace} />
      {source.containers.length > 0 && (
        <>
          {' ('}
          <Names names={source.containers} />)
        </>
      )}
      {source.previous && ', previous instance'}
    </>
  );
}

// The moment is drawn as the backend stamped it.
function anchorLine(anchor: Anchor): string {
  if (anchor.kind === 'Head') return 'from the start';
  if (anchor.kind === 'Tail') return 'at the newest line';
  return `from ${anchor.at ?? ''}`;
}

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
