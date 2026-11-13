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

import { type Formatter } from 'react-timeago';

import { RelativeTime } from '@/components/primitives/relative-time';

const SECOND_MS = 1_000;

// Countdown to the next retry ("in 1m 30s"); "now" once due.
const countdownFormatter: Formatter = (_value, _unit, _suffix, epochMs) => {
  const totalSec = Math.floor(Math.max(0, epochMs - Date.now()) / SECOND_MS);
  if (totalSec < 1) return 'now';
  if (totalSec < 60) return `in ${totalSec}s`;
  return `in ${Math.floor(totalSec / 60)}m ${totalSec % 60}s`;
};

// "in 1m 30s", ticking each second, then "now".
export function Countdown({ ms }: { ms: number }) {
  return <RelativeTime ms={ms} formatter={countdownFormatter} />;
}
