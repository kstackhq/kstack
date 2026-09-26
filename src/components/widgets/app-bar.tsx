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
// The drag strip is common; only the ends differ, and this is the one place that
// decides which. macOS reserves its native traffic lights and keeps the OS menu bar
// and caption buttons; the frameless platforms get `AppMenu` on the left and
// `WindowControls` on the right.
// See docs/adr/2026-08-09-per-platform-window-chrome.md
import { cn } from '@kubetail/ui/lib/utils';

import { AccountAvatar } from '@/components/widgets/account-avatar';
import { APP_BAR_CONTROL_CLASS } from '@/components/widgets/app-bar-button';
import { AppMenu } from '@/components/widgets/app-menu';
import { ContextOmnibox } from '@/components/widgets/context-omnibox';
import { HistoryNav } from '@/components/widgets/history-nav';
import { HomeButton } from '@/components/widgets/home-button';
import { NotificationButton } from '@/components/widgets/notification-button';
import { SettingsButton } from '@/components/widgets/settings-button';
import { WindowControls } from '@/components/widgets/window-controls';
import { isMacOS } from '@/lib/platform';

// Space (px) reserved for the macOS traffic lights: the host places the first at
// x=20 (`window_manager.rs`) and the three end near x=72; the rest is breathing
// room. Eyeball against the native buttons when tuning.
const TRAFFIC_LIGHT_GUTTER = 96;

export function AppBar() {
  // `AppMenu` registers the `Ctrl/Cmd+N`/`Q` accelerators while it is mounted, so
  // this branch is what keeps them off macOS, where the native menu owns them.
  const mac = isMacOS();

  return (
    // 56px tall; `window_manager.rs` mirrors the number to center the traffic lights
    // in it. No padding of its own: the close button has to reach the window's
    // corner, and the gutter supplies the macOS inset.
    //
    // `deep` makes every pixel of the bar a drag handle, spacing and margins
    // included, rather than only the elements that carry the attribute. Tauri's
    // hit test walks up from what was pressed and stops at the first clickable
    // element, so the buttons in here still click; the omnibox opts out below.
    //
    // `transform-gpu` pins the band to a retained compositor layer — without it
    // WebKitGTK re-rasters during an interactive resize and the transparent Linux
    // window flashes through to the desktop. It costs subpixel text rendering
    // (`window-frame.tsx`), acceptable only while the bar carries no body text.
    <div
      data-testid="app-bar"
      data-tauri-drag-region="deep"
      className="flex h-14 shrink-0 transform-gpu items-stretch bg-topbar"
    >
      {mac ? (
        <div
          data-testid="traffic-light-gutter"
          aria-hidden
          className="shrink-0"
          style={{ width: TRAFFIC_LIGHT_GUTTER }}
        />
      ) : (
        <AppMenu />
      )}
      {/* 16px from the chrome on the left, 8px between the two controls. */}
      <div data-testid="app-bar-controls" className="ml-4 flex items-center gap-2">
        <HomeButton />
        <HistoryNav />
      </div>
      {/* The omnibox rides between two spacers, so it centers itself in whatever
          the two ends leave rather than against a fixed offset. They keep 16px
          once the window is too narrow to leave them any, so the omnibox never
          butts against the chrome. */}
      <div data-testid="app-bar-spacer" aria-hidden className="min-w-4 flex-1" />
      {/* Starts at the omnibox's own width and takes a third of whatever slack
          the row has left, so it widens with the window until the omnibox hits
          its own cap. */}
      <div className="flex min-w-0 basis-104 grow items-center justify-center">
        <ContextOmnibox />
      </div>
      <div data-testid="app-bar-spacer" aria-hidden className="min-w-4 flex-1" />
      {/* 20px from the window's right edge, or from the caption buttons where the
          platform draws them; 8px between the two, as on the left. */}
      <div data-testid="app-bar-actions" className="mr-5 flex items-center gap-2 self-center">
        {/* Grouped as one pill, like `HistoryNav`'s back/forward pair, since the two
            read as a unit in the design rather than two separate controls. */}
        <div className={cn('flex h-9 shrink-0 items-center border', APP_BAR_CONTROL_CLASS)}>
          <NotificationButton />
          <div className="h-4.5 w-px shrink-0 bg-sidebar-border" aria-hidden />
          <SettingsButton />
        </div>
        <AccountAvatar />
      </div>
      {!mac && <WindowControls />}
    </div>
  );
}
