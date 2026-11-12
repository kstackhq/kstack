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

import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { describe, expect, it, onTestFinished, vi } from 'vitest';

import type { ClusterCachedDataObject } from '@/lib/cluster-cached-data-objects';
import { stubLayout } from '@/test-utils';

import { ObjectsTable } from './objects-table';

// ObjectsTable is pure presentation over the rows its page hands it. `live` is a watch
// whose snapshot has landed with these objects.
const live = (...objects: unknown[]) => ({
  objects: objects as ClusterCachedDataObject[],
  active: true,
  phase: 'live' as const,
});

const PODS = { apiVersion: 'v1', resource: 'pods', kind: 'Pod', label: 'Pods', namespaced: true };
// A CRD kind with no registered columns — exercises the universal-columns fallback.
const WIDGETS = {
  apiVersion: 'example.com/v1',
  resource: 'widgets',
  kind: 'Widget',
  label: 'Widgets',
  namespaced: true,
};

function obj(over: Record<string, unknown> = {}) {
  return {
    uid: 'u1',
    apiVersion: 'v1',
    kind: 'Pod',
    namespace: 'default',
    name: 'my-pod',
    creationTimestamp: '2026-07-20T10:00:00Z',
    rawJSON: undefined as unknown,
    ...over,
  };
}

const restarts = (n: number) => ({ spec: { containers: [{}] }, status: { containerStatuses: [{ restartCount: n }] } });

// Rows render only inside a laid-out viewport (see VirtualTable).
stubLayout(400);

describe('ObjectsTable', () => {
  it('explains the unsynced state when there is no active cache', () => {
    render(<ObjectsTable {...PODS} objects={[]} active={false} phase="connecting" />);
    expect(screen.getByText(/No synced cache/i)).toBeInTheDocument();
  });

  it('shows a spinner while connecting', () => {
    render(<ObjectsTable {...PODS} objects={[]} active phase="connecting" />);
    expect(screen.getByText(/Loading pod/i)).toBeInTheDocument();
  });

  it('shows an empty state on a connected empty snapshot', () => {
    render(<ObjectsTable {...PODS} {...live()} />);
    expect(screen.getByText('No pod objects.')).toBeInTheDocument();
  });

  it('falls back to the universal Name/Namespace/Age columns for a kind with no registry entry', () => {
    render(<ObjectsTable {...WIDGETS} {...live(obj({ kind: 'Widget' }))} />);
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent);
    expect(headers).toEqual(['Namespace', 'Name', 'Age']);
    expect(screen.getByText('my-pod')).toBeInTheDocument();
    expect(screen.getByText('default')).toBeInTheDocument();
  });

  it("renders a CRD's declared columns between Name and Age", () => {
    render(
      <ObjectsTable
        {...WIDGETS}
        printerColumns={[{ name: 'Replicas', type: 'integer', jsonPath: '.spec.replicas', priority: 0 }]}
        {...live(obj({ kind: 'Widget', rawJSON: { spec: { replicas: 3 } } }))}
      />,
    );

    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).toEqual([
      'Namespace',
      'Name',
      'Replicas',
      'Age',
    ]);
    expect(screen.getByText('3')).toBeInTheDocument();
  });

  // The page filters the rows; the table drops the column every row would repeat.
  it('omits the Namespace column under a namespace scope', () => {
    render(<ObjectsTable {...PODS} {...live(obj())} namespace="default" />);
    expect(screen.getAllByRole('columnheader').map((h) => h.textContent)).not.toContain('Namespace');
  });

  it('names the namespace in a scoped empty state', () => {
    render(<ObjectsTable {...PODS} {...live()} namespace={'a\u200bb'} />);
    expect(screen.getByText(/No pod objects in/)).toHaveTextContent('No pod objects in a\\u{200B}b.');
  });

  it('omits the Namespace column for a cluster-scoped kind', () => {
    render(
      <ObjectsTable
        apiVersion="v1"
        resource="nodes"
        kind="Node"
        label="Nodes"
        namespaced={false}
        {...live(obj({ namespace: '', name: 'node-1' }))}
      />,
    );
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent);
    expect(headers).toEqual(['Name', 'Status', 'Roles', 'Version', 'Age']);
  });

  it('renders Pod-specific columns (Status/Ready/Restarts/Node) computed from the body', () => {
    const pod = obj({
      rawJSON: {
        spec: { nodeName: 'pool-a-2', containers: [{}, {}] },
        status: {
          phase: 'Running',
          containerStatuses: [
            { ready: true, restartCount: 1 },
            { ready: false, restartCount: 2 },
          ],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const headers = screen.getAllByRole('columnheader').map((h) => h.textContent);
    expect(headers).toEqual(['Namespace', 'Name', 'Status', 'Ready', 'Restarts', 'Node', 'Age']);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('1/2')).toBeInTheDocument(); // ready / total containers
    // Running, but not ready: pending until it is.
    expect(within(row).getByText('Running').closest('[data-tone]')).toHaveAttribute('data-tone', 'pending');
    expect(within(row).getByText('3')).toHaveClass('text-destructive'); // summed restarts
    expect(within(row).getByText('pool-a-2')).toBeInTheDocument();
  });

  it('mutes a pod with no restarts, and dashes one with no node or status yet', () => {
    const pod = obj({ rawJSON: { spec: { containers: [{}] }, status: { containerStatuses: [{ restartCount: 0 }] } } });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('0')).toHaveClass('text-muted-foreground');
    // Status and Node.
    expect(within(row).getAllByText('—')).toHaveLength(2);
  });

  it('draws the name over the workload that owns it, through VisibleText', () => {
    const pod = obj({
      name: 'web\u200b-1',
      rawJSON: {
        metadata: {
          labels: { 'pod-template-hash': '7d9f' },
          ownerReferences: [{ kind: 'ReplicaSet', name: 'web-7d9f', controller: true }],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    expect(screen.getByText(/^web/, { selector: '.font-mono' })).toHaveTextContent('web\\u{200B}-1');
    expect(screen.getByText('deploy/web')).toBeInTheDocument();
  });

  it('prints an age as kubectl does, largest unit only', () => {
    const ago = (ms: number) => new Date(Date.now() - ms).toISOString();
    render(
      <ObjectsTable
        {...PODS}
        {...live(
          obj({ uid: 'a', name: 'a', creationTimestamp: ago(22 * 60_000 + 5_000) }),
          obj({ uid: 'b', name: 'b', creationTimestamp: ago(3 * 86_400_000) }),
        )}
      />,
    );
    // Age is the last column.
    const age = (name: string) => screen.getByText(name).closest('tr')!.lastElementChild;
    expect(age('a')).toHaveTextContent(/^22m$/);
    expect(age('b')).toHaveTextContent(/^3d$/);
  });

  it('counts the rows it shows in its footer', () => {
    render(<ObjectsTable {...PODS} {...live(obj(), obj({ uid: 'u2', name: 'other' }))} />);
    expect(screen.getByText('Showing 2 of 2 pods')).toBeInTheDocument();
  });

  // kubectl derives a pod's Status from container state, not `status.phase` — a crash-looping
  // pod is phase=Running, so trusting the phase would report "Running" for a broken pod.
  it('reports a waiting container reason over the coarse phase', () => {
    const pod = obj({
      rawJSON: {
        spec: { containers: [{}] },
        status: {
          phase: 'Running',
          containerStatuses: [{ ready: false, restartCount: 5, state: { waiting: { reason: 'CrashLoopBackOff' } } }],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('CrashLoopBackOff').closest('[data-tone]')).toHaveAttribute('data-tone', 'failing');
    expect(within(row).queryByText('Running')).not.toBeInTheDocument();
  });

  it('reports init-container progress while a pod is initializing', () => {
    const pod = obj({
      rawJSON: {
        spec: { containers: [{}], initContainers: [{ name: 'setup' }, { name: 'migrate' }] },
        status: {
          phase: 'Pending',
          initContainerStatuses: [{ name: 'setup', state: { waiting: { reason: 'PodInitializing' } } }],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('Init:0/2')).toBeInTheDocument();
  });

  it('reports Terminating for a pod with a deletion timestamp', () => {
    const pod = obj({
      rawJSON: {
        metadata: { deletionTimestamp: '2026-07-21T10:00:00Z' },
        spec: { containers: [{}] },
        status: { phase: 'Running', containerStatuses: [{ ready: true, state: { running: {} } }] },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('Terminating')).toBeInTheDocument();
  });

  // Retries during init land in initContainerStatuses while containerStatuses is still empty —
  // reading only the latter would show "—" despite a nonzero restart count.
  it('counts init-container restarts in the Restarts total', () => {
    const pod = obj({
      rawJSON: {
        spec: { containers: [{}], initContainers: [{ name: 'setup' }] },
        status: {
          phase: 'Pending',
          initContainerStatuses: [
            { name: 'setup', restartCount: 3, state: { waiting: { reason: 'CrashLoopBackOff' } } },
          ],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('3')).toBeInTheDocument();
  });

  it('sums init and regular container restarts', () => {
    const pod = obj({
      rawJSON: {
        spec: { containers: [{}], initContainers: [{ name: 'setup' }] },
        status: {
          phase: 'Running',
          initContainerStatuses: [{ name: 'setup', restartCount: 2, state: { terminated: { exitCode: 0 } } }],
          containerStatuses: [{ ready: true, restartCount: 4, state: { running: {} } }],
        },
      },
    });
    render(<ObjectsTable {...PODS} {...live(pod)} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('6')).toBeInTheDocument();
  });

  it('degrades kind-specific cells to "—" when the body is missing', () => {
    // A registered kind (Pod) still shows its columns, but with no body every cell is "—".
    render(<ObjectsTable {...PODS} {...live(obj())} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    // Status, Ready, Restarts and Node all fall back to "—" (Age has a real timestamp here).
    expect(within(row).getAllByText('—')).toHaveLength(4);
  });

  it('renders "—" for a null creationTimestamp instead of an ancient date', () => {
    render(<ObjectsTable {...WIDGETS} {...live(obj({ kind: 'Widget', creationTimestamp: null }))} />);
    const row = screen.getByText('my-pod').closest('tr')!;
    expect(within(row).getByText('—')).toBeInTheDocument();
    expect(screen.queryByText(/0001/)).not.toBeInTheDocument();
  });

  it('renders the live rows through the virtualized, fixed-layout table', () => {
    render(<ObjectsTable {...PODS} {...live(obj())} />);
    document.querySelectorAll('table').forEach((t) => expect(t).toHaveClass('table-fixed'));
  });

  describe('sorting', () => {
    const pods = [
      obj({ uid: 'a', name: 'a', creationTimestamp: '2026-07-20T10:00:00Z', rawJSON: restarts(0) }),
      obj({ uid: 'b', name: 'b', creationTimestamp: '2026-07-22T10:00:00Z', rawJSON: restarts(7) }),
      obj({ uid: 'c', name: 'c', creationTimestamp: '2026-07-21T10:00:00Z', rawJSON: restarts(2) }),
    ];
    const names = () =>
      screen
        .getAllByRole('row')
        .filter((r) => r.querySelector('td'))
        .map((r) => r.querySelector('.font-mono')?.textContent);
    const press = (header: string) => fireEvent.click(screen.getByRole('button', { name: header }));

    it('keeps the watch’s order until a header is pressed, and says so', () => {
      render(<ObjectsTable {...PODS} {...live(...pods)} />);
      expect(names()).toEqual(['a', 'b', 'c']);
      expect(screen.getByText(/^Sorted by/)).toHaveTextContent('Sorted by namespace');
    });

    it('sorts a count largest first, then reverses, then unsorts', () => {
      render(<ObjectsTable {...PODS} {...live(...pods)} />);
      press('Restarts');
      expect(names()).toEqual(['b', 'c', 'a']);
      expect(screen.getByText(/^Sorted by/)).toHaveTextContent('Sorted by restarts');
      press('Restarts');
      expect(names()).toEqual(['a', 'c', 'b']);
      press('Restarts');
      expect(names()).toEqual(['a', 'b', 'c']);
    });

    it('sorts by a running job’s duration again as it grows, with no new rows', () => {
      onTestFinished(() => {
        vi.useRealTimers();
      });
      vi.useFakeTimers();
      vi.setSystemTime(Date.parse('2026-01-01T01:00:00Z'));
      const JOBS = { apiVersion: 'batch/v1', resource: 'jobs', kind: 'Job', label: 'Jobs', namespaced: true };
      const jobs = [
        obj({ uid: 'r', name: 'running', rawJSON: { status: { startTime: '2026-01-01T00:59:30Z' } } }),
        obj({
          uid: 'd',
          name: 'done',
          rawJSON: { status: { startTime: '2026-01-01T00:00:00Z', completionTime: '2026-01-01T00:01:00Z' } },
        }),
      ];
      render(<ObjectsTable {...JOBS} {...live(...jobs)} />);
      press('Duration');
      expect(names()).toEqual(['done', 'running']);
      act(() => {
        vi.advanceTimersByTime(60_000);
      });
      expect(names()).toEqual(['running', 'done']);
    });

    it('sorts text and age smallest first', () => {
      render(<ObjectsTable {...PODS} {...live(...pods)} />);
      press('Name');
      press('Name');
      expect(names()).toEqual(['c', 'b', 'a']);
      press('Age');
      expect(names()).toEqual(['b', 'c', 'a']);
    });

    it('reads a sort by a column it no longer draws as unsorted', () => {
      const { rerender } = render(<ObjectsTable {...PODS} {...live(...pods)} />);
      press('Namespace');
      rerender(<ObjectsTable {...PODS} {...live(...pods)} namespace="default" />);
      expect(screen.getByText(/^Sorted by/)).toHaveTextContent('Sorted by name');
      expect(screen.getAllByRole('columnheader').every((h) => !h.hasAttribute('aria-sort'))).toBe(true);
    });

    it('keeps an object without the value last', () => {
      const ageless = obj({ uid: 'z', name: 'z', creationTimestamp: null });
      render(<ObjectsTable {...PODS} {...live(ageless, ...pods)} />);
      press('Age');
      expect(names()).toEqual(['b', 'c', 'a', 'z']);
    });
  });

  describe('status tabs', () => {
    const pod = (uid: string, rawJSON: unknown) => obj({ uid, name: uid, rawJSON });
    const ready = { status: { phase: 'Running', conditions: [{ type: 'Ready', status: 'True' }] } };
    const crashing = {
      status: { phase: 'Running', containerStatuses: [{ state: { waiting: { reason: 'CrashLoopBackOff' } } }] },
    };
    const starting = { status: { phase: 'Pending' } };
    const pods = [pod('a', ready), pod('b', crashing), pod('c', ready), pod('d', starting)];
    const tab = (name: RegExp) => screen.getByRole('tab', { name });

    it('counts each status in its colour', () => {
      render(<ObjectsTable {...PODS} {...live(...pods)} />);
      expect(screen.getAllByRole('tab').map((t) => t.textContent)).toEqual([
        'All4',
        'Healthy2',
        'Failing1',
        'Pending1',
      ]);
      expect(within(tab(/^All/)).getByText('4')).toHaveClass('text-muted-foreground');
      expect(within(tab(/^Healthy/)).getByText('2')).toHaveClass('text-emerald-600');
      expect(within(tab(/^Failing/)).getByText('1')).toHaveClass('text-red-600');
      expect(within(tab(/^Pending/)).getByText('1')).toHaveClass('text-amber-600');
    });

    it('shows a tab’s pods alone, and counts them in the footer', async () => {
      render(<ObjectsTable {...PODS} {...live(...pods)} />);
      fireEvent.click(tab(/^Healthy/));
      await waitFor(() => expect(screen.getByText('Showing 2 of 4 pods')).toBeInTheDocument());
      expect(screen.queryByText('b')).not.toBeInTheDocument();
    });

    it('draws an empty table for a tab with nothing in it, and keeps the tabs', async () => {
      render(<ObjectsTable {...PODS} {...live(pod('a', ready))} />);
      fireEvent.click(tab(/^Failing/));
      await waitFor(() => expect(screen.getByText('Showing 0 of 1 pods')).toBeInTheDocument());
      expect(screen.getAllByRole('columnheader').length).toBeGreaterThan(0);
      expect(screen.queryByText(/^No /)).not.toBeInTheDocument();
      expect(within(tab(/^Failing/)).getByText('0')).toHaveClass('text-muted-foreground');
    });

    it('draws no tabs for a kind without a status', () => {
      render(<ObjectsTable {...WIDGETS} {...live(obj({ kind: 'Widget' }))} />);
      expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    });
  });
});
