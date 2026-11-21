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

// The left sidebar's width, which the sidebar and the app bar's gutter both draw. One
// reader holds it (`AppLayout`) and hands it to both: a second `usePersistedWidth` of
// the same key would not see a drag until the next window.
import { usePersistedWidth } from '@/lib/persisted-width';

// Drag-resize bounds (px); `initial` matches the library's 16rem.
const WIDTH = { min: 200, max: 480, initial: 256 };

export function useLeftSidebarWidth(): [number, (px: number) => void] {
  return usePersistedWidth('sidebar-width', WIDTH);
}
