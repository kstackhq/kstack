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
// over a row of the mode rail beside the main card, which holds the left sidebar, the
// routed page and the right sidebar. Secondary windows get their own sidebar-less
// layout alongside this one.
//
// Every band is a flex item, so the sidebar and the page start below the chrome by
// construction. The column's height is definite (`h-`, not `min-h-`) and `main`
// clips, so a route scrolls inside it, never the page. Every plain flex item between
// here and a route's scroller needs `min-h-0` to shrink into it.
import { SidebarProvider } from '@kubetail/ui/elements/sidebar';
import { Outlet, useLocation } from '@tanstack/react-router';
import { useRef } from 'react';

import { ChatNav } from '@/components/chat/chat-nav';
import { DashboardResourceNav } from '@/components/dashboard/dashboard-resource-nav';
import { AppBar } from '@/components/shell/app-bar';
import { AppDialogs } from '@/components/shell/app-dialogs';
import { ConnectionStatus } from '@/components/shell/connection-status';
import { LeftSidebar } from '@/components/shell/left-sidebar';
import { ModeRail } from '@/components/shell/mode-rail';
import { RightSidebar, pageMinWidth } from '@/components/shell/right-sidebar';
import type { AppMode } from '@/lib/app-mode';
import { ChatOutboxProvider } from '@/lib/chat-outbox';
import { ChatSwitchProvider } from '@/lib/chat-switch';
import { ClusterCachedDataKindsProvider } from '@/lib/cluster-cached-data-kinds';
import { ClusterNamespacesProvider } from '@/lib/cluster-namespaces';
import { DialogProvider } from '@/lib/dialog';
import { useLeftSidebarWidth } from '@/lib/left-sidebar-width';
import { LogWindowsProvider } from '@/lib/log-windows';
import { LogsViewProvider } from '@/lib/logs-view';
import { ObjectViewProvider } from '@/lib/object-view';
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
  // Read once here: the sidebar and the bar's gutter draw the same width, live.
  const [sidebarWidth, setSidebarWidth] = useLeftSidebarWidth();
  // The right sidebar measures the page to stop a drag at the page's floor.
  const pageRef = useRef<HTMLElement>(null);

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
            {/* The focused view and the focused object open the right sidebar when
            set, so they sit inside the sidebar's state and above the row that draws it. */}
            <ObjectViewProvider>
              <LogsViewProvider>
                {/* The log windows move the focused view out of the sidebar, so they sit
              inside both. */}
                <LogWindowsProvider>
                  {/* Above the bar, the sidebar and the routes: the nav, the bar's title
                and the page read the catalog, and the bar's namespace chip reads the
                namespaces on every page. */}
                  <ClusterCachedDataKindsProvider enabled={onDashboard}>
                    <ClusterNamespacesProvider enabled={onDashboard}>
                      {/* The column is the chrome: the bar, the rail and the 8px around the
                      main card show its colour. */}
                      <SidebarProvider
                        open={sidebarOpen}
                        onOpenChange={setSidebarOpen}
                        className="h-(--app-min-h) min-h-0 flex-col bg-border"
                      >
                        <AppBar mode={mode} sidebarWidth={sidebarWidth} />
                        {/* `overflow-clip`: the items' minimum widths can sum past a narrow
                        window. */}
                        <div className="flex min-h-0 flex-1 overflow-clip">
                          <ModeRail />
                          {/* 8px between the rail and the card, and of chrome right of and
                          below it; the bar above is the gap there. */}
                          <div className="flex min-h-0 min-w-0 flex-1 px-2 pb-2">
                            {/* `relative`: too narrow for the sidebar beside the page,
                            `LeftSidebar` floats it over the card as a drawer, and an
                            absolute element anchors here rather than past `main`'s clip. */}
                            <div className="relative flex min-h-0 min-w-0 flex-1 overflow-clip rounded-xl border bg-background shadow-md">
                              <LeftSidebar
                                width={sidebarWidth}
                                onResize={setSidebarWidth}
                                nav={onDashboard ? <DashboardResourceNav /> : <ChatNav mode="chat" />}
                              />
                              <main
                                ref={pageRef}
                                className="flex min-h-0 flex-1 flex-col overflow-hidden bg-background"
                                style={{ minWidth: pageMinWidth }}
                              >
                                <Outlet />
                              </main>
                              <RightSidebar page={pageRef} />
                            </div>
                          </div>
                        </div>
                      </SidebarProvider>
                    </ClusterNamespacesProvider>
                  </ClusterCachedDataKindsProvider>
                </LogWindowsProvider>
              </LogsViewProvider>
            </ObjectViewProvider>
          </RightSidebarProvider>
          <AppDialogs />
        </DialogProvider>
      </ChatSwitchProvider>
    </ChatOutboxProvider>
  );
}
