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

// The app bar's session control: one avatar, in both states. A session is an
// add-on, not a gate, so the app runs signed out too — the menu then offers Sign
// in instead of Account/Sign out, rather than swapping the control for a
// differently-shaped button.
import { LogOut, User, UserRound } from 'lucide-react';

import { AppBarButton } from '@/components/widgets/app-bar-button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';

import { invoke } from '@tauri-apps/api/core';

import { useAuthState } from '@/lib/auth';

/** Host command the menu drives. Keep in sync with `commands.rs`. */
const OPEN_ACCOUNT_URL_CMD = 'open_account_url';

// Initials for the avatar, from a name or an email local part.
function initials(s: string): string {
  // Split on separators too, so `andres.morey@…` yields `AM`, not `AN`.
  const trimmed = s.trim();
  if (!trimmed) return '?';
  const parts = trimmed.split(/[\s._-]+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return trimmed.slice(0, 2).toUpperCase();
}

export function AccountAvatar() {
  const { authState, loading, login, logout } = useAuthState();
  const { identity, authenticated } = authState;
  // email/name are non-null strings (empty = absent), so `||`, not `??`.
  const label = identity?.name || identity?.email || null;

  return (
    <DropdownMenu>
      {/* A filled circle rather than a ghost icon button: the avatar is an identity,
          not a chrome control, so it carries its own accent fill like the sidebar's
          account row (`left-sidebar-card.tsx`), whether or not there's a session. Its
          hover still lands on the bar's own wash, like every other control in it. */}
      <DropdownMenuTrigger
        render={<AppBarButton className="rounded-full border-transparent bg-accent text-accent-foreground" />}
        aria-label={`Account: ${label ?? (authenticated ? 'Signed in' : 'Guest')}`}
      >
        {label ? <span className="text-xs font-medium">{initials(label)}</span> : <UserRound aria-hidden />}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" sideOffset={4} className="min-w-44">
        {authenticated ? (
          <>
            {/* The account page lives on the web; the host opens it in the browser. */}
            <DropdownMenuItem onClick={() => invoke(OPEN_ACCOUNT_URL_CMD).catch(() => {})}>
              <UserRound className="size-4" aria-hidden />
              Account
            </DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => logout().catch(() => {})}>
              <LogOut className="size-4" aria-hidden />
              Sign out
            </DropdownMenuItem>
          </>
        ) : (
          <DropdownMenuItem disabled={loading} onClick={() => login().catch(() => {})}>
            <User className="size-4" aria-hidden />
            Sign in
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
