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

// The mode rail: a column at the window's left edge, below the app bar. The modes at
// its top are router links, so each is a deep-linkable route and the active one is
// marked by the router's `data-status`. Notifications and Settings sit at its foot.
// It is there at every width, open sidebar or not: it is how you switch modes.
import { Link } from '@tanstack/react-router';
import { LayoutDashboard, MessageCircle } from 'lucide-react';

import { RAIL_ITEM_CLASS } from './button';
import { NotificationButton } from './notification-button';
import { SettingsButton } from './settings-button';

export function ModeRail() {
  return (
    // 38px: an 8px inset and the 30px buttons. The bottom group sits 12px above the
    // window's bottom edge; the top one is level with the main card's top.
    <div data-testid="mode-rail" className="flex w-9.5 shrink-0 flex-col justify-between pb-3 pl-2">
      <nav aria-label="View" className="flex flex-col gap-1">
        <Link to="/chat" aria-label="Chat" title="Chat" className={RAIL_ITEM_CLASS}>
          <MessageCircle aria-hidden />
        </Link>
        <Link to="/dashboard" aria-label="Dashboard" title="Dashboard" className={RAIL_ITEM_CLASS}>
          <LayoutDashboard aria-hidden />
        </Link>
      </nav>
      <div className="flex flex-col gap-1">
        <NotificationButton />
        <SettingsButton />
      </div>
    </div>
  );
}
