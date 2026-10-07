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

// The main window's layout, mounted once by the pathless `_app` route: the app bar
// over a row holding the floating sidebar beside the routed page. Secondary windows
// get their own sidebar-less layout alongside this one.
//
// Every band is a flex item, so the sidebar and the page start below the chrome by
// construction. The column's height is definite (`h-`, not `min-h-`) and `main`
// clips, so a route scrolls inside it, never the page. Every plain flex item between
// here and a route's scroller needs `min-h-0` to shrink into it.
import { Outlet, useLocation } from '@tanstack/react-router';

import { SidebarProvider } from '@kubetail/ui/elements/sidebar';

import { AppBar } from '@/components/widgets/app-bar';
import { AppDialogs } from '@/components/widgets/app-dialogs';
import { LeftSidebar } from '@/components/widgets/left-sidebar';
import { ChatNav } from '@/components/widgets/chat-nav';
import { DashboardResourceNav } from '@/components/widgets/dashboard-resource-nav';
import { ModeNav } from '@/components/widgets/mode-nav';
import { RightSidebar, RightSidebarToggle } from '@/components/widgets/right-sidebar';
import type { AppMode } from '@/lib/app-mode';
import { ChatOutboxProvider } from '@/lib/chat-outbox';
import { ChatSwitchProvider } from '@/lib/chat-switch';
import { ConnectionStatus } from '@/lib/connection-status';
import { DialogProvider } from '@/lib/dialog';
import { usePersistedFlag } from '@/lib/persisted-flag';
import { RightSidebarProvider } from '@/lib/right-sidebar';

export function AppLayout() {
  // Each mode's nav mounts only in that mode. This layout stays mounted across
  // the mode switch, so subscribe via `useLocation` (re-renders per navigation) —
  // `useMatchRoute` would read stale until a reload.
  const pathname = useLocation({ select: (location) => location.pathname });
  const mode: AppMode = pathname === '/dashboard' || pathname.startsWith('/dashboard/') ? 'dashboard' : 'chat';
  const onDashboard = mode === 'dashboard';
  // The library's provider persists open/collapsed to a `sidebar_state` cookie it
  // never reads back, so hold the state here to keep it across windows.
  const [sidebarOpen, setSidebarOpen] = usePersistedFlag('sidebar-open', true);

  return (
    // The dialogs host sits outside the sidebar and the bar alike; the controls in
    // them only request an open.
    // The chat outbox lives here, above the routes: the first send moves chat mode to
    // the new chat's own route, which unmounts the pane that held the text. A sandbox
    // or network switch in flight is held beside it for the same reason.
    <ChatOutboxProvider>
      <ChatSwitchProvider>
        <DialogProvider>
          <ConnectionStatus />
          {/* The right sidebar's state wraps the bar and the row alike: its toggle is
          in one, its panel in the other. The library's provider owns the left
          sidebar's narrow-window state and `Cmd/Ctrl+B`; it hardcodes `min-h-svh`,
          taller than the `WindowFrame` inset on Linux, and only `min-h-0` displaces
          it (same tailwind-merge group) — `h-` alone would not. */}
          <RightSidebarProvider mode={mode}>
            <SidebarProvider
              open={sidebarOpen}
              onOpenChange={setSidebarOpen}
              className="h-(--app-min-h) min-h-0 flex-col"
            >
              <AppBar />
              {/* `relative`: too narrow for the card beside the page, `LeftSidebar`
              floats it over this row. */}
              <div className="relative flex min-h-0 flex-1">
                {/* The mode switch is pinned: it is how you leave whatever the nav below
                is showing, so it must not scroll away with it. */}
                <LeftSidebar
                  header={<ModeNav />}
                  nav={onDashboard ? <DashboardResourceNav /> : <ChatNav mode="chat" />}
                />
                <main className="flex min-h-0 flex-1 flex-col overflow-hidden bg-background">
                  <Outlet />
                </main>
                <RightSidebar />
                {/* Pinned to the row's top-right corner rather than to the page, so it
                holds its place as the panel opens under it — over the page while
                closed, over the panel once open. Below the sidebar drawer's z-30
                scrim: a covered control must not be clickable.
                The 16px inset mirrors where the card's own toggle lands (`p-2` on
                the card plus `p-2` on its header), so the two sit level. */}
                <div className="absolute top-4 right-4 z-20">
                  <RightSidebarToggle />
                </div>
              </div>
            </SidebarProvider>
          </RightSidebarProvider>
          <AppDialogs />
        </DialogProvider>
      </ChatSwitchProvider>
    </ChatOutboxProvider>
  );
}
