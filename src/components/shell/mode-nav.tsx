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

// Chat/Dashboard switch in the app bar: a segmented control of icons. Router links, not
// local state, so each mode is a real deep-linkable route; the active one is highlighted
// via `data-status`.
import { Tooltip, TooltipContent, TooltipTrigger } from '@kubetail/ui/elements/tooltip';
import { Link } from '@tanstack/react-router';
import { LayoutDashboard, MessageCircle } from 'lucide-react';
import type { LucideIcon } from 'lucide-react';

import { APP_BAR_BOX_CLASS } from '@/lib/control-style';

const TRACK = `flex items-center gap-0.5 bg-muted p-0.75 ${APP_BAR_BOX_CLASS}`;
const SEGMENT =
  'flex size-5.5 items-center justify-center rounded-[5px] border border-transparent text-muted-foreground transition-colors hover:text-foreground ' +
  'data-[status=active]:border-[0.5px] data-[status=active]:border-sidebar-border data-[status=active]:bg-background data-[status=active]:text-foreground data-[status=active]:shadow-sm';

function Segment({ to, label, icon: Icon }: { to: '/chat' | '/dashboard'; label: string; icon: LucideIcon }) {
  return (
    <Tooltip>
      <TooltipTrigger render={<Link to={to} aria-label={label} className={SEGMENT} />}>
        <Icon className="size-3.75" aria-hidden />
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

export function ModeNav() {
  return (
    // The whole track is the control: a press between the icons must not drag the window.
    <nav className={TRACK} aria-label="View" data-tauri-drag-region="false">
      <Segment to="/chat" label="Chat" icon={MessageCircle} />
      <Segment to="/dashboard" label="Dashboard" icon={LayoutDashboard} />
    </nav>
  );
}
