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

// The library's surface. The fake client is left out: a test imports it by its file, and so
// does the one place in the app that mounts it.
export { LogViewerCore, createCoreStore } from './core';
export type { CoreHandle, CoreRow, CoreRows, CoreStore, LogViewerCoreProps } from './core';
export { CORE_INITIAL_STATE, LogWindow } from './window';
export type { CoreState, Edge, EdgeState, Effect, LogWindowOptions } from './window';
export type { Anchor, Client, Cursor, FetchMode, FetchQuery, FollowFrame, LogLine, Page, PageEdge } from './types';
