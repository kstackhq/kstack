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

// The collapsed sidebar: a small vertical pill carrying the toggle back and the
// mode switch as icons. Router links, like `ModeNav`, so a mode stays a real
// deep-linkable route and the active one highlights off `data-status`.

import { Link } from '@tanstack/react-router';
import { LayoutDashboard, MessageCircle } from 'lucide-react';

import { LeftSidebarToggle } from '@/components/widgets/left-sidebar-toggle';

// `size-7` matches the toggle's `icon-sm`, so the pill is one column wide.
const ITEM =
  'flex size-7 items-center justify-center rounded-full text-muted-foreground transition-colors ' +
  'hover:bg-sidebar-accent hover:text-foreground ' +
  'data-[status=active]:bg-sidebar-accent data-[status=active]:text-foreground';

export function LeftSidebarPill() {
  return (
    // Same `p-2` footprint as the open card, so collapsing changes the sidebar's
    // width and nothing else about the row.
    <div data-testid="left-sidebar-pill" className="shrink-0 p-2">
      <div className="flex flex-col items-center gap-1 rounded-lg bg-card p-1 shadow-lg ring-1 ring-sidebar-border">
        <LeftSidebarToggle />
        <Link to="/chat" aria-label="Chat" className={ITEM}>
          <MessageCircle className="size-4 " aria-hidden />
        </Link>
        <Link to="/dashboard" aria-label="Dashboard" className={ITEM}>
          <LayoutDashboard className="size-4 " aria-hidden />
        </Link>
      </div>
    </div>
  );
}
