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

// The window's right sidebar: flush against the app bar and the window's bottom
// edge, unlike the floating card on the left — it is a wall of the window, not a
// panel over the page. Closed until its toggle asks for it; the layout pins that
// toggle to the content row's top-right corner, so it holds its place as the panel
// opens under it.
//
// What it holds follows the mode, since the two have nothing in common: the dashboard
// gets a chat of its own, chat mode the focused log view, or a placeholder while
// none is set. The panel does not scroll — its contents own a scroller each, and one
// inside another gives two scrollbars that fight.
import { PanelRight } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';

import type { AppMode } from '@/lib/app-mode';
import { DashboardChat } from '@/components/widgets/dashboard-chat';
import { LogsViewPanel } from '@/components/widgets/logs-view-panel';
import { ResizeHandle } from '@/components/widgets/resize-handle';
import { useLogsView } from '@/lib/logs-view';
import { type WidthBounds, usePersistedWidth } from '@/lib/persisted-width';
import { useRightSidebar } from '@/lib/right-sidebar';

// Drag-resize bounds (px), per mode like the width itself: the two panels hold different
// things. Chat mode's holds the log viewer, whose columns want room; the dashboard's holds
// a transcript. `initial` is wide enough for a transcript to read on first open.
const WIDTH: Record<AppMode, WidthBounds> = {
  chat: { min: 240, max: 1200, initial: 400 },
  dashboard: { min: 240, max: 640, initial: 400 },
};
const widthKey = (mode: AppMode) => `right-sidebar-width:${mode}`;

function Placeholder({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex flex-col gap-1 p-4">
      <h2 className="text-sm font-semibold">{title}</h2>
      <p className="text-sm text-muted-foreground">{detail}</p>
    </div>
  );
}

function ChatPanel() {
  const { view, viewport, clear } = useLogsView();
  if (view) return <LogsViewPanel view={view} viewport={viewport} onClose={clear} />;
  return <Placeholder title="Conversation" detail="What the chat is working from will appear here." />;
}

export function RightSidebarToggle() {
  const { open, toggle } = useRightSidebar();

  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Toggle right sidebar"
      aria-pressed={open}
      onClick={toggle}
      className="text-muted-foreground"
    >
      <PanelRight aria-hidden />
    </Button>
  );
}

export function RightSidebar() {
  const { mode, open } = useRightSidebar();
  const [width, setWidth] = usePersistedWidth(widthKey(mode), WIDTH[mode]);
  if (!open) return null;

  const onDashboard = mode === 'dashboard';

  return (
    // An ordinary item in the content row, so it takes its width from the page
    // rather than covering it, and the row's height makes it flush top and bottom.
    // `relative` anchors the resize handle on its inner edge. It shrinks before the page
    // does (`app-layout.tsx` gives `<main>` its floor), down to its minimum; the stored width
    // is left alone for a wider window.
    <aside
      data-testid="right-sidebar"
      aria-label={onDashboard ? 'Dashboard chat' : 'Chat details'}
      className="relative flex flex-col overflow-hidden bg-card"
      style={{ width, minWidth: WIDTH[mode].min }}
    >
      {onDashboard ? <DashboardChat /> : <ChatPanel />}
      {/* The panel ends at the window's right edge, so what is left of the window
          from the pointer is its width. */}
      <ResizeHandle edge="left" onResize={setWidth} label="Resize right sidebar" />
    </aside>
  );
}
