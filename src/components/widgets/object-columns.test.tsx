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

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ClusterCachedDataObject } from '@/lib/cluster-cached-data-objects';
import { columnsForKind } from './object-columns';
import type { ObjectColumn } from './object-columns';

const widgetGVR = { apiVersion: 'example.com/v1', resource: 'widgets' };

function objectWith(rawJSON: unknown): ClusterCachedDataObject {
  return {
    uid: 'u-1',
    namespace: 'default',
    name: 'widget-1',
    creationTimestamp: '',
    rawJSON,
  } as ClusterCachedDataObject;
}

// Render a cell to text, since a column's cell may be an element (a date renders a time-ago).
function cellText(column: ObjectColumn, o: ClusterCachedDataObject): string {
  const { container } = render(<>{column.cell(o)}</>);
  return container.textContent ?? '';
}

describe('columnsForKind', () => {
  it('gives an unregistered kind with no descriptors nothing', () => {
    expect(columnsForKind(widgetGVR, [])).toEqual([]);
  });

  // A hand-written accessor exists because it says what a jsonPath cannot — podStatus reads
  // container state to correct a crash-looping pod that reports phase Running.
  it('prefers the hand-written registry over descriptors', () => {
    const columns = columnsForKind({ apiVersion: 'v1', resource: 'pods' }, [
      { name: 'Phase', type: 'string', jsonPath: '.status.phase', priority: 0 },
    ]);

    expect(columns.map((c) => c.header)).toEqual(['Ready', 'Status', 'Restarts']);
  });

  it('builds columns from descriptors in declared order', () => {
    const columns = columnsForKind(widgetGVR, [
      { name: 'Replicas', type: 'integer', jsonPath: '.spec.replicas', priority: 0 },
      { name: 'Phase', type: 'string', jsonPath: '.status.phase', priority: 0 },
    ]);

    expect(columns.map((c) => c.header)).toEqual(['Replicas', 'Phase']);
    // Under the table's fixed layout an unsized column would split the slack with Name.
    columns.forEach((c) => expect(c.className).toMatch(/\bw-\d+\b/));
    const o = objectWith({ spec: { replicas: 3 }, status: { phase: 'Running' } });
    expect(cellText(columns[0], o)).toBe('3');
    expect(cellText(columns[1], o)).toBe('Running');
  });

  // kubectl hides these behind -o wide; a table with no wide toggle should not show them.
  it('drops columns above priority 0', () => {
    const columns = columnsForKind(widgetGVR, [
      { name: 'Phase', type: 'string', jsonPath: '.status.phase', priority: 0 },
      { name: 'Node', type: 'string', jsonPath: '.spec.nodeName', priority: 1 },
    ]);

    expect(columns.map((c) => c.header)).toEqual(['Phase']);
  });

  it('renders a boolean and a missing value', () => {
    const columns = columnsForKind(widgetGVR, [
      { name: 'Paused', type: 'boolean', jsonPath: '.spec.paused', priority: 0 },
      { name: 'Missing', type: 'string', jsonPath: '.spec.nope', priority: 0 },
    ]);

    const o = objectWith({ spec: { paused: false } });
    expect(cellText(columns[0], o)).toBe('false');
    expect(cellText(columns[1], o)).toBe('—');
  });

  // A path resolving to a container renders nothing rather than "[object Object]".
  it('renders a non-scalar as absent', () => {
    const columns = columnsForKind(widgetGVR, [{ name: 'Spec', type: 'string', jsonPath: '.spec', priority: 0 }]);

    expect(cellText(columns[0], objectWith({ spec: { replicas: 3 } }))).toBe('—');
  });

  it('renders a date as its age, and a date that does not parse as absent', () => {
    const columns = columnsForKind(widgetGVR, [
      { name: 'Started', type: 'date', jsonPath: '.status.startedAt', priority: 0 },
    ]);

    const started = new Date(Date.now() - 5 * 60_000).toISOString();
    expect(cellText(columns[0], objectWith({ status: { startedAt: started } }))).toMatch(/5 minutes ago/);
    expect(cellText(columns[0], objectWith({ status: { startedAt: 'soon' } }))).toBe('—');
  });

  it('survives a partial body, which a Deleted row carries', () => {
    const columns = columnsForKind(widgetGVR, [
      { name: 'Replicas', type: 'integer', jsonPath: '.spec.replicas', priority: 0 },
    ]);

    expect(cellText(columns[0], objectWith(undefined))).toBe('—');
  });
});

describe('pod status', () => {
  const status = columnsForKind({ apiVersion: 'v1', resource: 'pods' })[1];
  const failedInit = (terminated: Record<string, unknown>) =>
    objectWith({
      spec: { initContainers: [{ name: 'setup' }] },
      status: { phase: 'Pending', initContainerStatuses: [{ name: 'setup', state: { terminated } }] },
    });

  // kubectl's printPod: the reason, else the signal, else the exit code.
  it('names a failed init container by its reason, signal or exit code', () => {
    expect(cellText(status, failedInit({ reason: 'Error', exitCode: 1 }))).toBe('Init:Error');
    expect(cellText(status, failedInit({ signal: 9, exitCode: 137 }))).toBe('Init:Signal:9');
    expect(cellText(status, failedInit({ exitCode: 2 }))).toBe('Init:ExitCode:2');
  });
});

describe('deployment columns', () => {
  const columns = columnsForKind({ apiVersion: 'apps/v1', resource: 'deployments' });

  it('counts ready of desired, up-to-date and available replicas', () => {
    const o = objectWith({
      spec: { replicas: 3 },
      status: { readyReplicas: 2, updatedReplicas: 3, availableReplicas: 1 },
    });
    expect(columns.map((c) => c.header)).toEqual(['Ready', 'Up-to-date', 'Available']);
    expect(columns.map((c) => cellText(c, o))).toEqual(['2/3', '3', '1']);
  });

  it('reads a body with no status as none ready', () => {
    expect(columns.map((c) => cellText(c, objectWith({})))).toEqual(['0/0', '0', '0']);
  });
});
