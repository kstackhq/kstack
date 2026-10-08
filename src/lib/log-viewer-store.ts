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

export type LogLine = {
  /** RFC 3339, as the backend stamps it. */
  ts: string;
  /** The pod the line came from, in its namespace. */
  namespace: string;
  pod: string;
  container: string;
  /** Whether the line is the instance before the last restart's. */
  previous: boolean;
  /** Text, with terminal control sequences stripped by the data layer. */
  message: string;
};

export type ViewerState = {
  /** The timestamps of the first and last visible lines; null before any line is drawn. */
  range: { from: string; to: string } | null;
  /** Whether the newest line is on screen. */
  atEnd: boolean;
  /** Whether the viewer keeps the end in view as lines arrive. Starts as the action says; the user's hand changes it. */
  pinnedToEnd: boolean;
  /** How many lines are on screen. */
  visible: number;
  /** The lines the user selected, in order. */
  selection: LogLine[];
};

export type ViewerStore = {
  getState: () => ViewerState;
  subscribe: (listener: () => void) => () => void;
  /** The viewer's write. Nothing else calls it. */
  set: (next: ViewerState) => void;
};

export const EMPTY_VIEWER_STATE: ViewerState = {
  range: null,
  atEnd: true,
  pinnedToEnd: false,
  visible: 0,
  selection: [],
};

export function createViewerStore(): ViewerStore {
  let state = EMPTY_VIEWER_STATE;
  const listeners = new Set<() => void>();
  return {
    getState: () => state,
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
