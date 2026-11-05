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

// Chat/Dashboard switch. Router links, not local state, so each mode is a real
// deep-linkable route; the active one is highlighted via `data-status`.
import { Link } from '@tanstack/react-router';
import { LayoutDashboard, MessageCircle } from 'lucide-react';

const TRACK = 'inline-flex w-full items-center rounded-lg bg-muted p-[3px] text-muted-foreground';
const ITEM =
  'flex flex-1 items-center justify-center gap-1.5 rounded-md px-2 py-1 text-sm font-medium transition-colors hover:text-foreground ' +
  'data-[status=active]:bg-background data-[status=active]:text-foreground data-[status=active]:shadow-sm';

export function ModeNav() {
  return (
    <nav className={TRACK} aria-label="View">
      <Link to="/chat" className={ITEM}>
        <MessageCircle className="size-4" aria-hidden />
        Chat
      </Link>
      <Link to="/dashboard" className={ITEM}>
        <LayoutDashboard className="size-4" aria-hidden />
        Dashboard
      </Link>
    </nav>
  );
}
