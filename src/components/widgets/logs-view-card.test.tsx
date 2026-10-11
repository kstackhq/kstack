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
import { describe, expect, it } from 'vitest';

import type { AppMode } from '@/lib/app-mode';
import type { ChatToolCall } from '@/lib/chats';
import type { LogsViewAction } from '@/lib/logs-view';
import { LogsViewProvider, useLogsView } from '@/lib/logs-view';
import { RightSidebarProvider } from '@/lib/right-sidebar';
import { LogsViewCard } from './logs-view-card';

const action: LogsViewAction = {
  sources: [
    { namespace: 'prod', kind: 'Deployment', name: 'web‮app', containers: ['app'], previous: false },
    { namespace: 'prod', kind: 'Pod', name: 'db-0', containers: [], previous: true },
  ],
  filters: [],
  grep: 'error',
  anchor: { kind: 'At', at: '2026-10-08T14:02:00Z' },
  pinToEnd: true,
};

const call = {
  id: 'call-1',
  toolUseID: 'tu-1',
  name: 'LogsView',
  contract: '',
  arguments: {},
  status: 'Succeeded',
  runsOn: 'Sidecar',
  actionKind: 'LogsView',
  action: {
    description: 'Tail the app',
    command: null,
    read: null,
    write: null,
    edit: null,
    search: null,
    fetch: null,
    memory: null,
    delegate: null,
    kubeQuery: null,
    logsView: action,
  },
  approval: null,
  network: null,
  output: 'Viewing deployments/webapp in prod from 2026-10-08T14:02:00Z, pinned to the end.',
  background: null,
  agentCallID: null,
  clusterWrites: [],
} as ChatToolCall;

// What the focused view is, as the card set it.
function Focused() {
  const { view } = useLogsView();
  return <output data-testid="focused">{view ? `${view.chatId}/${view.callId}` : 'none'}</output>;
}

const draw = (mode: AppMode = 'chat') =>
  render(
    <RightSidebarProvider mode={mode}>
      <LogsViewProvider>
        <LogsViewCard call={call} action={action} chatID="chat-1" mode={mode} />
        <Focused />
      </LogsViewProvider>
    </RightSidebarProvider>,
  );

describe('LogsViewCard', () => {
  it('names the view, where it opened, the description and the receipt, every name spelled out', () => {
    const { container } = draw();
    expect(container).toHaveTextContent('Logs: Deployment web');
    expect(container).toHaveTextContent('app in prod (app) and 1 more');
    expect(container).toHaveTextContent('from 2026-10-08T14:02:00Z, pinned to the end, matching /error/');
    expect(container).toHaveTextContent('“Tail the app”');
    expect(container).toHaveTextContent(call.output);
    expect(container.querySelector('mark')).not.toBeNull();
  });

  it('Expand makes the call the focused view, and the card then says so', () => {
    draw();
    expect(screen.getByTestId('focused')).toHaveTextContent('none');

    fireEvent.click(screen.getByRole('button', { name: 'Expand' }));

    expect(screen.getByTestId('focused')).toHaveTextContent('chat-1/call-1');
    expect(screen.queryByRole('button', { name: 'Expand' })).not.toBeInTheDocument();
    expect(screen.getByText('Showing in the sidebar')).toBeInTheDocument();
  });

  it('has no Expand on the dashboard', () => {
    draw('dashboard');
    expect(screen.queryByRole('button', { name: 'Expand' })).not.toBeInTheDocument();
    expect(screen.queryByText('Showing in the sidebar')).not.toBeInTheDocument();
  });
});
