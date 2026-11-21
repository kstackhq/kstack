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

// The open sidebar: a drag-resizable column at the main card's left, beside the page,
// on the chrome's tint. Its fill is its edge: the page's own fill starts where it
// ends. `LeftSidebar` mounts it while the sidebar is open. Built here rather than with
// the library's `Sidebar` so a collapse is an instant unmount, not a slide.
import { SidebarContent } from '@kubetail/ui/elements/sidebar';
import type { ReactNode } from 'react';

import { ResizeHandle } from '@/components/primitives/resize-handle';

type LeftSidebarCardProps = {
  /** Padding included; the layout holds it (`useLeftSidebarWidth`). */
  width: number;
  onResize: (px: number) => void;
  /** Sidebar body (navigation, pickers), in a scroller of its own. */
  nav?: ReactNode;
};

export function LeftSidebarCard({ width, onResize, nav }: LeftSidebarCardProps) {
  return (
    // An ordinary item in the main card's row: the row's height bounds it, and the
    // page takes whatever width is left. `relative` anchors the resize handle.
    <div data-testid="left-sidebar-card" className="relative flex shrink-0 bg-app-bar p-2" style={{ width }}>
      <SidebarContent className="min-h-0">{nav}</SidebarContent>
      <ResizeHandle edge="right" onResize={onResize} label="Resize sidebar" />
    </div>
  );
}
