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

// The app bar: the layout's top band and the window's title bar on every platform.
// Left to right: the gutter over the rail and the sidebar, the cluster chip, the page's
// title, then the dashboard's namespace scope and the right sidebar's toggle. The drag
// strip is common; only the ends differ, and this is the one place that decides which. macOS reserves its native traffic lights and keeps the OS menu bar
// and caption buttons; the frameless platforms get `AppMenu` on the left and
// `WindowControls` on the right.
// See docs/adr/2026-08-09-per-platform-window-chrome.md
import { useSidebar } from '@kubetail/ui/elements/sidebar';

import { NamespacePicker } from '@/components/dashboard/dashboard-resource-nav';
import { TrafficLightGutter } from '@/components/primitives/traffic-light-gutter';
import { WindowControls } from '@/components/primitives/window-controls';
import { ContextOmnibox } from '@/components/shell/context-omnibox';
import { RightSidebarToggle } from '@/components/shell/right-sidebar';
import type { AppMode } from '@/lib/app-mode';
import { isMacOS } from '@/lib/platform';
import { dismissPopupsOnWindowDrag } from '@/lib/window-drag';

import { AppBarDivider } from './button';
import { HistoryNav } from './history-nav';
import { LeftSidebarToggle } from './left-sidebar-toggle';
import { AppMenu } from './menu';
import { AppBarTitle } from './title';

// What lies left of the page while the sidebar is open: the mode rail's 38px, its 8px
// gap and the main card's 1px border (`app-layout.tsx`), so the gutter ends where the
// page starts.
const PAGE_INSET = 38 + 8 + 1;

type AppBarProps = {
  mode: AppMode;
  /** The left sidebar's width, which the gutter spans while it is open. */
  sidebarWidth: number;
};

export function AppBar({ mode, sidebarWidth }: AppBarProps) {
  // `AppMenu` registers the `Ctrl/Cmd+N`/`Q` accelerators while it is mounted, so
  // this branch is what keeps them off macOS, where the native menu owns them.
  const mac = isMacOS();
  const { open, isMobile } = useSidebar();
  // A drawer covers the page rather than pushing it, so then the gutter hugs.
  const gutterWidth = open && !isMobile ? sidebarWidth + PAGE_INSET : undefined;

  return (
    // 56px tall; `window_manager.rs` mirrors the number to center the traffic lights
    // in it. No padding of its own: the close button has to reach the window's
    // corner, and the gutter supplies the macOS inset.
    //
    // `deep` makes every pixel of the bar a drag handle, spacing and margins
    // included, rather than only the elements that carry the attribute. Tauri's
    // hit test walks up from what was pressed and stops at the first clickable
    // element, so the buttons in here still click; the two chips opt out. A press
    // that drags never ends in a click, so `dismissPopupsOnWindowDrag` clicks for
    // it, closing any open popup.
    //
    // `transform-gpu` pins the band to a retained compositor layer — without it
    // WebKitGTK re-rasters during an interactive resize and the transparent Linux
    // window flashes through to the desktop. The layer paints the chrome's colour
    // itself for the same reason. It costs subpixel text rendering
    // (`window-frame.tsx`): the title and breadcrumbs draw grayscale-antialiased,
    // which beats a window that flashes on every resize.
    <div
      data-testid="app-bar"
      data-tauri-drag-region="deep"
      onPointerDown={dismissPopupsOnWindowDrag}
      className="flex h-14 shrink-0 transform-gpu items-stretch bg-border"
    >
      <div
        data-testid="app-bar-gutter"
        className="flex shrink-0 items-center gap-1.5 pr-3"
        style={{ width: gutterWidth }}
      >
        {mac ? <TrafficLightGutter /> : <AppMenu />}
        <HistoryNav />
        <LeftSidebarToggle />
      </div>
      <div data-testid="app-bar-divider" className="flex items-center">
        <AppBarDivider />
      </div>
      <div className="flex min-w-0 items-center pl-1.5">
        <ContextOmnibox />
      </div>
      <AppBarTitle mode={mode} />
      <div data-testid="app-bar-spacer" aria-hidden className="min-w-4 flex-1" />
      {mode === 'dashboard' && (
        <div className="flex min-w-0 items-center">
          <NamespacePicker />
        </div>
      )}
      {/* 12px from the window's right edge, or from the caption buttons. */}
      <div className="flex items-center pr-3 pl-1">
        <RightSidebarToggle />
      </div>
      {!mac && <WindowControls />}
    </div>
  );
}
