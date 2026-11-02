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

import { DataRateSparkline, SPARK_H, SPARK_W } from './data-rate-sparkline';

const sample = (received: number, sent: number) => ({ receivedBytesPerSecond: received, sentBytesPerSecond: sent });
// A series of `slots` slots holding samples; the sidecar's interval is 2s.
const series = (slots: number, ...samples: ReturnType<typeof sample>[]) => ({
  intervalSeconds: 2,
  windowSeconds: 2 * slots,
  samples,
});

// Each line's points as [x, y] pairs: received first, then sent.
function linesOf(container: HTMLElement): [number, number][][] {
  return [...container.querySelectorAll('polyline')].map((line) =>
    line
      .getAttribute('points')!
      .split(' ')
      .map((p) => p.split(',').map(Number) as [number, number]),
  );
}

describe('DataRateSparkline', () => {
  it('draws nothing for an empty series', () => {
    const { container } = render(<DataRateSparkline series={series(60)} />);

    expect(container.querySelector('svg')).toBeNull();
  });

  it('draws a line for each direction, hidden from assistive tech', () => {
    const { container } = render(<DataRateSparkline series={series(60, sample(1, 2), sample(3, 4))} />);

    expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
    expect(linesOf(container)).toHaveLength(2);
  });

  // The newest sample sits at the right edge; a short history leaves the left empty.
  it('places the newest sample at the right edge, one slot per sample', () => {
    const { container } = render(<DataRateSparkline series={series(5, sample(0, 0), sample(0, 0))} />);

    const [received] = linesOf(container);
    expect(received.map(([x]) => x)).toEqual([(3 / 4) * SPARK_W, SPARK_W]);
  });

  // One scale for both lines, so they read against each other.
  it('draws both lines on one scale', () => {
    const { container } = render(<DataRateSparkline series={series(60, sample(100, 50))} />);

    const [received, sent] = linesOf(container);
    const top = 1;
    const bottom = SPARK_H - 1;
    expect(received[0][1]).toBe(top);
    expect(sent[0][1]).toBe((top + bottom) / 2);
  });

  it('draws an idle connection as two flat lines on the baseline', () => {
    const { container } = render(<DataRateSparkline series={series(60, sample(0, 0), sample(0, 0))} />);

    expect(linesOf(container).map((line) => line.map(([, y]) => y))).toEqual([
      [SPARK_H - 1, SPARK_H - 1],
      [SPARK_H - 1, SPARK_H - 1],
    ]);
  });
});
