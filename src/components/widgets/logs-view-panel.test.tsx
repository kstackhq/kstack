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
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import type { LogsView } from '@/lib/logs-view';
import { createViewerStore } from '@/lib/log-viewer-store';
import { LogsViewPanel } from './logs-view-panel';

// The viewer has a suite of its own; this file is about the panel around it.
vi.mock('@/components/widgets/log-viewer', () => ({
  LogViewer: ({ openAt, pinToEnd }: { openAt: { kind: string; at: string | null }; pinToEnd: boolean }) => (
    <div data-testid="log-viewer" data-open-at={openAt.at ?? openAt.kind} data-pinned={pinToEnd} />
  ),
}));

const view: LogsView = {
  chatId: 'chat-1',
  callId: 'call-1',
  action: {
    sources: [
      { namespace: 'prod', kind: 'Deployment', name: 'webapp', containers: ['app', 'istio-proxy'], previous: false },
      { namespace: 'prod', kind: 'Pod', name: 'work\u202Eer-1', containers: [], previous: true },
    ],
    filters: [{ field: 'Node', values: ['node-a', 'node-b'] }],
    grep: 'error|warn',
    anchor: { kind: 'At', at: '2026-10-08T14:02:00Z' },
    pinToEnd: false,
  },
};

describe('LogsViewPanel', () => {
  it('names the first source and how many more, then every source, the filters and the grep', () => {
    render(<LogsViewPanel view={view} viewer={createViewerStore()} onClose={() => {}} />);
    expect(screen.getByRole('heading')).toHaveTextContent(
      'Logs: Deployment webapp in prod (app, istio-proxy) and 1 more',
    );
    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent('Deployment webapp in prod (app, istio-proxy)');
    expect(items[1]).toHaveTextContent(/, previous instance$/);
    expect(screen.getByText(/^node:/)).toHaveTextContent('node: node-a, node-b');
    expect(screen.getByText(/matching/)).toHaveTextContent('matching /error|warn/');
    expect(screen.getByText(/^from /)).toHaveTextContent('from 2026-10-08T14:02:00Z');
  });

  it('says where the view opens and whether it is pinned', () => {
    const tail = { ...view, action: { ...view.action, anchor: { kind: 'Tail' as const, at: null }, pinToEnd: true } };
    render(<LogsViewPanel view={tail} viewer={createViewerStore()} onClose={() => {}} />);
    expect(screen.getByText(/newest line/)).toHaveTextContent('at the newest line, pinned to the end');

    const head = { ...view, action: { ...view.action, anchor: { kind: 'Head' as const, at: null } } };
    render(<LogsViewPanel view={head} viewer={createViewerStore()} onClose={() => {}} />);
    expect(screen.getByText(/^from the start$/)).toBeInTheDocument();
  });

  it('spells a reordering character in a name out, never drawing it', () => {
    render(<LogsViewPanel view={view} viewer={createViewerStore()} onClose={() => {}} />);
    expect(screen.getByTitle('An invisible character')).toBeInTheDocument();
  });

  it('draws one source alone, with no filter and no grep lines', () => {
    render(
      <LogsViewPanel
        view={{
          ...view,
          action: { ...view.action, sources: [view.action.sources[0]], filters: [], grep: '' },
        }}
        viewer={createViewerStore()}
        onClose={() => {}}
      />,
    );
    expect(screen.getByRole('heading')).toHaveTextContent(/^Logs: Deployment webapp in prod \(app, istio-proxy\)$/);
    expect(screen.queryByText(/^node:/)).not.toBeInTheDocument();
    expect(screen.queryByText(/matching/)).not.toBeInTheDocument();
  });

  it('closes from its button', () => {
    const onClose = vi.fn();
    render(<LogsViewPanel view={view} viewer={createViewerStore()} onClose={onClose} />);
    fireEvent.click(screen.getByRole('button', { name: 'Close log view' }));
    expect(onClose).toHaveBeenCalledOnce();
  });

  it('mounts the viewer at the action’s anchor, pinned as the action says', () => {
    render(<LogsViewPanel view={view} viewer={createViewerStore()} onClose={() => {}} />);
    expect(screen.getByTestId('log-viewer')).toHaveAttribute('data-open-at', '2026-10-08T14:02:00Z');
    expect(screen.getByTestId('log-viewer')).toHaveAttribute('data-pinned', 'false');
  });
});
