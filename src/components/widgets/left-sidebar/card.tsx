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

// The open sidebar: a rounded, drag-resizable card at the left of the layout's
// content row, beside the page. `LeftSidebar` mounts it while the sidebar is open
// and swaps in `LeftSidebarPill` otherwise. Built here rather than with the library's
// `Sidebar` so the swap is an instant unmount, not a slide.
import { SidebarContent, SidebarHeader } from '@kubetail/ui/elements/sidebar';
import type { ReactNode } from 'react';

import { AppLogo } from '@/components/widgets/app-logo';
import { ResizeHandle } from '@/components/widgets/resize-handle';
import { usePersistedWidth } from '@/lib/persisted-width';

import { LeftSidebarToggle } from './toggle';

// Drag-resize bounds (px); `initial` matches the library's 16rem.
const WIDTH = { min: 200, max: 480, initial: 256 };
const WIDTH_KEY = 'sidebar-width';

// The card's `p-2` padding (px): the visible border sits this far inside the card's
// right edge, and the drag math offsets by it so grabbing the handle doesn't jump.
const CARD_PADDING = 8;

type LeftSidebarCardProps = {
  /** Pinned above the scroll area, under the logo and toggle row. */
  header?: ReactNode;
  /** Sidebar body (navigation, pickers) — scrolls under the header. */
  nav?: ReactNode;
};

export function LeftSidebarCard({ header, nav }: LeftSidebarCardProps) {
  const [width, setWidth] = usePersistedWidth(WIDTH_KEY, WIDTH);

  return (
    // An ordinary item in the content row: the row's height bounds the card, and the
    // page takes whatever width is left. `relative` anchors the resize handle.
    <div data-testid="left-sidebar-card" className="relative flex shrink-0 p-2" style={{ width }}>
      <div className="flex size-full min-h-0 flex-col rounded-lg bg-sidebar shadow-sm ring-1 ring-sidebar-border">
        {/* The header is the card's fixed top — its `p-2` is the padding the card
            shows above and either side of it. The nav scrolls beneath, padded to
            match but flush with the bottom edge, so a long list runs off it. */}
        <SidebarHeader>
          <div className="flex items-center justify-between">
            <AppLogo />
            <LeftSidebarToggle />
          </div>
          {header}
        </SidebarHeader>
        <SidebarContent className="px-2">{nav}</SidebarContent>
      </div>
      <ResizeHandle edge="right" offset={CARD_PADDING} onResize={setWidth} label="Resize sidebar" />
    </div>
  );
}
