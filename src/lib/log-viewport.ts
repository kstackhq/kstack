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

// What the log viewer knows that nothing else does, readable on demand. The viewer
// writes its state here as it changes; a reader pulls it once when it needs it —
// the composer at send, for the snapshot that rides the question — so nothing
// streams the viewer's state anywhere. The shape is useSyncExternalStore's.

import type { CoreState, LogLine } from '@/lib/log-viewer';

export type LogViewport = Pick<CoreState, 'range' | 'atEnd' | 'pinnedToEnd' | 'visible'> & {
  /** The lines the user selected, in order. */
  selection: LogLine[];
};

export type LogViewportHandle = {
  get: () => LogViewport;
  subscribe: (listener: () => void) => () => void;
  /** The viewer's write. Nothing else calls it. */
  set: (next: LogViewport) => void;
};

export const EMPTY_LOG_VIEWPORT: LogViewport = {
  range: null,
  atEnd: false,
  pinnedToEnd: false,
  visible: 0,
  selection: [],
};

export function createLogViewportHandle(): LogViewportHandle {
  let state = EMPTY_LOG_VIEWPORT;
  const listeners = new Set<() => void>();
  return {
    get: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    set: (next) => {
      state = next;
      listeners.forEach((listener) => listener());
    },
  };
}
