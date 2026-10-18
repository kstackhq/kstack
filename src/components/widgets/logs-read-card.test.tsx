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
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AppMode } from '@/lib/app-mode';
import type { ChatToolCall } from '@/lib/chats';
import type { LogsReadAction } from '@/lib/logs-view';

// The card makes no client: any request it made would land here.
const { requested } = vi.hoisted(() => ({ requested: [] as unknown[] }));
vi.mock('urql', () => ({ useClient: () => requested.push('client') }));
vi.mock('@/lib/logs-client', () => ({ createLogsClient: () => requested.push('logs client') }));

const { LogsViewProvider, useLogsView } = await import('@/lib/logs-view');
const { RightSidebarProvider } = await import('@/lib/right-sidebar');
const { LogsReadCard } = await import('./logs-read-card');

beforeEach(() => {
  requested.length = 0;
  localStorage.clear();
});

const line = (i: number, message = `line ${i}`): LogsReadAction['lines'][number] => ({
  timestamp: `2026-10-17T09:14:${String(i).padStart(2, '0')}Z`,
  namespace: 'prod',
  pod: 'webapp-a',
  container: 'app',
  previous: false,
  message,
  gap: null,
});

const viewAround = {
  sources: [
    {
      namespace: 'prod',
      kind: 'Deployment',
      name: 'webapp',
      containers: ['app'],
      allContainers: false,
      previous: false,
    },
  ],
  filters: [],
  grep: 'error',
  anchor: { kind: 'At', at: '2026-10-17T09:14:00Z' },
  pinToEnd: false,
  resolvedAt: '2026-10-17T10:00:00Z',
} as LogsReadAction['viewAround'];

const action: LogsReadAction = {
  sources: viewAround!.sources,
  filters: [],
  grep: 'error',
  since: '2026-10-17T09:13:00Z',
  until: null,
  mode: 'Head',
  resolvedAt: '2026-10-17T10:00:00Z',
  lines: [
    line(0, '<b>bold</b> ‮reversed'),
    { ...line(1), gap: { after: '2026-10-17T09:14:00Z', before: '2026-10-17T09:14:01Z' } },
    ...Array.from({ length: 10 }, (_, i) => line(i + 2)),
  ],
  dropped: 3,
  before: 'Unread',
  after: 'More',
  skipped: ['webapp-b/app: forbidden'],
  viewAround,
};

const call = {
  id: 'call-1',
  toolUseID: 'tu-1',
  name: 'LogsRead',
  contract: '',
  arguments: {},
  status: 'Succeeded',
  runsOn: 'Sidecar',
  actionKind: 'LogsRead',
  action: { description: 'Why checkout failed', logsRead: action },
  approval: null,
  network: null,
  output: 'Read 12 lines',
  background: null,
  agentCallID: null,
  clusterWrites: [],
} as unknown as ChatToolCall;

function Focused() {
  const { view } = useLogsView();
  return (
    <output data-testid="focused">
      {view ? `${view.clusterId}/${view.chatId}/${view.callId}/${view.action.anchor.at}` : 'none'}
    </output>
  );
}

const draw = ({ mode = 'chat', read = action }: { mode?: AppMode; read?: LogsReadAction } = {}) =>
  render(
    <RightSidebarProvider mode={mode}>
      <LogsViewProvider>
        <LogsReadCard call={call} action={read} chatID="chat-1" clusterID="cluster-1" mode={mode} />
        <Focused />
      </LogsViewProvider>
    </RightSidebarProvider>,
  );

const rows = () => screen.getAllByTestId('logs-inline-row');

describe('LogsReadCard', () => {
  it('names what was read over which range, with the description', () => {
    const { container } = draw();
    expect(container).toHaveTextContent('Read: Deployment webapp in prod (app)');
    expect(container).toHaveTextContent('from 2026-10-17T09:13:00Z, oldest first, matching /error/');
    expect(container).toHaveTextContent('“Why checkout failed”');
  });

  it('draws the range’s two ends and a tail read’s mode', () => {
    const { container } = draw({ read: { ...action, since: null, until: '2026-10-17T09:15:00Z', mode: 'Tail' } });
    expect(container).toHaveTextContent('up to 2026-10-17T09:15:00Z, the newest');
  });

  it('draws the first eight rows from the action alone, holes included, and requests nothing', () => {
    draw();
    expect(rows()).toHaveLength(8);
    expect(screen.getByText(/lines may be missing/i)).toBeInTheDocument();
    expect(requested).toEqual([]);
  });

  it('shows every line on Show all', () => {
    draw();
    fireEvent.click(screen.getByRole('button', { name: 'Show all 12 lines' }));
    expect(rows()).toHaveLength(12);
    expect(screen.queryByRole('button', { name: /Show all/ })).not.toBeInTheDocument();
  });

  it('draws markup and reordering characters in a message as text', () => {
    const { container } = draw();
    expect(container.querySelector('b')).toBeNull();
    expect(container).toHaveTextContent('<b>bold</b>');
    expect(container.querySelector('mark')).not.toBeNull();
  });

  it('words what the model saw beyond the lines: the dropped, each edge, the skipped streams', () => {
    const { container } = draw();
    expect(container).toHaveTextContent('3 more lines were left out');
    expect(container).toHaveTextContent('older lines may exist');
    expect(container).toHaveTextContent('newer lines exist');
    expect(container).toHaveTextContent('could not read webapp-b/app: forbidden');
  });

  it('says nothing of an edge that is the end, and words a cursor’s side as more', () => {
    const { container } = draw({ read: { ...action, before: 'More', after: 'End', dropped: 0, skipped: [] } });
    expect(container).toHaveTextContent('older lines exist');
    expect(container).not.toHaveTextContent('newer lines');
    expect(container).not.toHaveTextContent('left out');
  });

  it('View around this focuses the read’s view around at this call', () => {
    draw();
    fireEvent.click(screen.getByRole('button', { name: 'View around this' }));
    expect(screen.getByTestId('focused')).toHaveTextContent('cluster-1/chat-1/call-1/2026-10-17T09:14:00Z');
    expect(screen.getByText('Showing in the sidebar')).toBeInTheDocument();
  });

  it('has no View around this on the dashboard, or for a read of no line', () => {
    draw({ mode: 'dashboard' });
    expect(screen.queryByRole('button', { name: 'View around this' })).not.toBeInTheDocument();
  });

  it('has no View around this for a read of no line, and says there were none', () => {
    const { container } = draw({ read: { ...action, lines: [], viewAround: null, dropped: 0 } });
    expect(screen.queryByRole('button', { name: 'View around this' })).not.toBeInTheDocument();
    expect(container).toHaveTextContent('No lines');
  });
});
