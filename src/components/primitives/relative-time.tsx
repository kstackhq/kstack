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

import ReactTimeAgo, { type Formatter } from 'react-timeago';

const SECOND_MS = 1_000;
const MINUTE_MS = 60 * SECOND_MS;
const HOUR_MS = 60 * MINUTE_MS;

// Live "Xs ago" counter rolling up to minutes/hours; no "just now" bucket.
const relativeFormatter: Formatter = (_value, _unit, _suffix, epochMs) => {
  const diff = Math.max(0, Date.now() - epochMs);
  if (diff < MINUTE_MS) return `${Math.floor(diff / SECOND_MS)}s ago`;
  if (diff < HOUR_MS) return `${Math.floor(diff / MINUTE_MS)}m ago`;
  return `${Math.floor(diff / HOUR_MS)}h ago`;
};

// maxPeriod={1} forces a per-second re-render; react-timeago otherwise throttles
// to once-a-minute above 60s, freezing a countdown.
export function RelativeTime({ ms, formatter = relativeFormatter }: { ms: number; formatter?: Formatter }) {
  return <ReactTimeAgo date={ms} component="span" formatter={formatter} maxPeriod={1} />;
}
