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
// The log viewer: the scroller over a view's lines. It takes the query as the
// record holds it, lands at `openAt` on mount and never again (the caller keys it
// on the call's id, so a new call is a fresh landing and a re-render of the same
// call moves nothing), starts pinned to the end or not as `pinToEnd` says, and
// writes what it knows into `store` for whoever pulls it. Lines arrive whether or
// not it is pinned; pinning is only whether the viewport keeps the end in view.
//
// The viewer is not in this repository yet: the props are its contract, and the
// body holds its place. Its data layer is a hook over the sidecar's GraphQL, never
// a transport of its own.
import type { LogsViewAction } from '@/lib/logs-view';
import type { ViewerStore } from '@/lib/log-viewer-store';

export type LogViewerProps = {
  query: LogsViewAction;
  /** Where the viewer lands on mount. */
  openAt: LogsViewAction['anchor'];
  /** Whether it starts pinned to the end. */
  pinToEnd: boolean;
  store: ViewerStore;
};

export function LogViewer(_: LogViewerProps) {
  return <p className="p-3 text-sm text-muted-foreground">The log viewer is not built yet.</p>;
}
