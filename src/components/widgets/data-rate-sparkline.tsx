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

import { seriesSlots } from '@/lib/graphql/use-watch-subscription';
import type { SeriesFrame } from '@/lib/graphql/use-watch-subscription';

// The viewBox. The SVG stretches it to the row, and the strokes keep their width.
export const SPARK_W = 120;
export const SPARK_H = 24;

type Rate = { receivedBytesPerSecond: number; sentBytesPerSecond: number };

// A connection's recent data rate: received and sent as two lines on one scale, one point per
// slot, the newest at the right edge. A sample after a sleep is just the next slot; nothing
// here reads a time. Nothing is drawn for an empty series. aria-hidden, since the text row
// beside it says the numbers.
export function DataRateSparkline({ series }: { series: SeriesFrame<Rate> }) {
  const { samples } = series;
  if (samples.length === 0) return null;

  const slots = seriesSlots(series);
  const max = Math.max(0, ...samples.flatMap((s) => [s.receivedBytesPerSecond, s.sentBytesPerSecond]));
  // Inset by a unit so a stroke on the baseline or the peak is not cut in half.
  const y = (v: number) => (max === 0 ? SPARK_H - 1 : SPARK_H - 1 - (v / max) * (SPARK_H - 2));
  const x = (i: number) => ((slots - samples.length + i) / Math.max(slots - 1, 1)) * SPARK_W;
  const points = (value: (s: Rate) => number) => samples.map((s, i) => `${x(i)},${y(value(s))}`).join(' ');

  return (
    <svg
      aria-hidden="true"
      viewBox={`0 0 ${SPARK_W} ${SPARK_H}`}
      preserveAspectRatio="none"
      className="ml-2 inline-block h-4 w-24 overflow-visible align-middle"
    >
      <polyline
        points={points((s) => s.receivedBytesPerSecond)}
        fill="none"
        stroke="var(--series-received)"
        strokeWidth={2}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
      <polyline
        points={points((s) => s.sentBytesPerSecond)}
        fill="none"
        stroke="var(--series-sent)"
        strokeWidth={2}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
