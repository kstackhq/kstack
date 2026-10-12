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
// What one log viewer has on screen, readable on demand. The viewer writes it here
// as it changes; a reader pulls it once when it needs it — the composer at send,
// for the snapshot that rides the question — so nothing streams it anywhere. It
// never holds the lines: those are the core's. The shape is useSyncExternalStore's.
import { createStore } from '@/lib/log-viewer';
import type { LogLine, Store } from '@/lib/log-viewer';

/** What the viewer has on screen, and the lines the user selected, in order. */
export type Viewport = {
  /** Timestamps of the first and last visible lines; null before any is drawn. */
  range: { from: string; to: string } | null;
  /** Whether the newest line is on screen. */
  atEnd: boolean;
  /** Whether the viewport keeps the end in view. The user's hand changes it. */
  pinnedToEnd: boolean;
  /** How many lines are on screen. */
  visible: number;
  selection: LogLine[];
};

/** The viewer's `set` is the one write. */
export type ViewportHandle = Store<Viewport>;

export const EMPTY_VIEWPORT: Viewport = {
  range: null,
  atEnd: false,
  pinnedToEnd: false,
  visible: 0,
  selection: [],
};

export function createViewportHandle(): ViewportHandle {
  return createStore(EMPTY_VIEWPORT);
}
