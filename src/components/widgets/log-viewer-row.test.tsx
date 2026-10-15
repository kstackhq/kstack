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

import { act, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { LogLine } from '@/lib/log-viewer';
import { LOG_ROW_H, LogViewerRow } from './log-viewer-row';

// Built in local time, since the row draws local time.
const local = (...parts: [number, number, number, number, number, number, number]) => new Date(...parts).toISOString();

const line = (over: Partial<LogLine> = {}): LogLine => ({
  timestamp: local(2026, 9, 15, 9, 5, 3, 123),
  cursor: 'c1',
  message: 'GET /healthz 200',
  stream: { namespace: 'prod', pod: 'webapp-7f9c', container: 'istio-proxy', previous: false },
  place: { node: 'n', region: '', zone: '', os: '', arch: '' },
  ...over,
});

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date(2026, 9, 15, 12, 0, 0));
});

afterEach(() => {
  vi.useRealTimers();
});

describe('LogViewerRow', () => {
  it('draws the time, the pod and the message', () => {
    render(<LogViewerRow line={line()} showContainer={false} landed={false} />);
    const row = screen.getByTestId('log-row');
    expect(row).toHaveTextContent('09:05:03.123');
    expect(row).toHaveTextContent('webapp-7f9c');
    expect(row).toHaveTextContent('GET /healthz 200');
    expect(row).not.toHaveTextContent('istio-proxy');
    expect(row).toHaveClass(LOG_ROW_H);
  });

  it('spells out what cluster text could hide', () => {
    render(<LogViewerRow line={line({ message: 'ok‮evil' })} showContainer={false} landed={false} />);
    expect(screen.getByTestId('log-row').querySelector('mark')).not.toBeNull();
  });

  it('puts the date before a time from another day', () => {
    render(
      <LogViewerRow
        line={line({ timestamp: local(2026, 9, 14, 23, 59, 59, 5) })}
        showContainer={false}
        landed={false}
      />,
    );
    expect(screen.getByTestId('log-row')).toHaveTextContent('Oct 14 23:59:59.005');
  });

  it('draws the milliseconds of a time with nine fraction digits', () => {
    const nine = line().timestamp.replace(/Z$/, '456789Z');
    render(<LogViewerRow line={line({ timestamp: nine })} showContainer={false} landed={false} />);
    expect(screen.getByTestId('log-row')).toHaveTextContent('09:05:03.123');
  });

  it('draws the container after the pod when asked', () => {
    render(<LogViewerRow line={line()} showContainer landed={false} />);
    expect(screen.getByTestId('log-row')).toHaveTextContent('webapp-7f9c/istio-proxy');
  });

  it('re-renders only when what it draws changes', () => {
    // The row reads the message on every render, so a getter counts its renders.
    let reads = 0;
    const counted = {
      ...line(),
      get message() {
        reads += 1;
        return 'GET /healthz 200';
      },
    };
    let setTop: (top: number) => void = () => {};
    let bump: () => void = () => {};
    function Parent() {
      const [top, set] = useState(0);
      const [, tick] = useState(0);
      setTop = set;
      bump = () => tick((n) => n + 1);
      return <LogViewerRow line={counted} showContainer={false} landed={false} top={top} />;
    }
    render(<Parent />);
    const mounted = reads;

    act(() => bump());
    expect(reads).toBe(mounted);

    act(() => setTop(20));
    expect(reads).toBeGreaterThan(mounted);
    expect(screen.getByTestId('log-row')).toHaveStyle({ transform: 'translateY(20px)' });
  });

  it('marks the row the view landed on', () => {
    const { rerender } = render(<LogViewerRow line={line()} showContainer={false} landed={false} />);
    expect(screen.getByTestId('log-row')).not.toHaveClass('border-l-2');
    rerender(<LogViewerRow line={line()} showContainer={false} landed />);
    expect(screen.getByTestId('log-row')).toHaveClass('border-l-2');
  });
});
