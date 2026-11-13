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

// The window's left sidebar, as the layout mounts it: the open `LeftSidebarCard` or the
// collapsed `LeftSidebarPill`. Too narrow for the card beside the page, the pill holds
// the row alone and the toggle opens the card over it as a drawer — so opening one
// never moves the page under the pointer, and narrowing never throws a panel over it.
//
// Open/collapsed state, the drawer's own state, and `Cmd/Ctrl+B` come from the
// library's `SidebarProvider`, mounted by `app-layout.tsx`; this reads them through
// `useSidebar`. Narrowness comes from there too (`isMobile`, its own hardcoded
// 768px) rather than a second media query of our own: it is what the toggle
// branches on, and a component rendering off a different rule would leave that
// button flipping a state nothing shows.
import { useSidebar } from '@kubetail/ui/elements/sidebar';
import { useLocation } from '@tanstack/react-router';
import type { ReactNode } from 'react';
import { useEffect } from 'react';

import { useFocusTrap } from '@/lib/focus-trap';

import { LeftSidebarCard } from './card';
import { LeftSidebarPill } from './pill';

type LeftSidebarProps = {
  /** Pinned above the scroll area, beside the toggle. */
  header?: ReactNode;
  /** Sidebar body (navigation, pickers) — scrolls under the header. */
  nav?: ReactNode;
};

export function LeftSidebar({ header, nav }: LeftSidebarProps) {
  const { open, isMobile, openMobile, setOpenMobile } = useSidebar();
  const drawer = isMobile && openMobile;
  const trapRef = useFocusTrap<HTMLDivElement>(drawer);

  // Escape closes it, as it does any overlay. The listener stays on `window`, which
  // is later in the bubble path than `document`: a dialog opened from inside the
  // drawer dismisses by stopping the keydown there, so one keypress closes the dialog
  // and leaves the drawer alone.
  useEffect(() => {
    if (!drawer) return undefined;
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpenMobile(false);
    };
    window.addEventListener('keydown', onKeyDown);
    return () => window.removeEventListener('keydown', onKeyDown);
  }, [drawer, setOpenMobile]);

  // A drawer left open covers the page it just opened, so a navigation closes it.
  // Keyed on the whole location: picking a resource moves the search param, not the
  // path.
  const href = useLocation({ select: (location) => location.href });
  useEffect(() => {
    setOpenMobile(false);
  }, [href, setOpenMobile]);

  if (!isMobile) return open ? <LeftSidebarCard header={header} nav={nav} /> : <LeftSidebarPill />;

  return (
    <>
      {/* The pill holds the row's width, so the page keeps its geometry while the
          card floats above it. Both anchor to the content row (`app-layout.tsx`
          positions it). */}
      <LeftSidebarPill />
      {drawer && (
        <>
          <button
            type="button"
            aria-label="Close sidebar"
            data-testid="sidebar-scrim"
            onClick={() => setOpenMobile(false)}
            className="absolute inset-0 z-30 cursor-default bg-black/40"
          />
          {/* A panel over the page has to hold the keyboard as well as the eye:
              the trap keeps Tab inside it and hands focus back on close, and
              `aria-modal` tells assistive tech to ignore what it covers. */}
          <div
            ref={trapRef}
            role="dialog"
            aria-modal
            aria-label="Sidebar"
            data-testid="sidebar-overlay"
            className="absolute inset-y-0 left-0 z-40 flex drop-shadow-xl"
          >
            <LeftSidebarCard header={header} nav={nav} />
          </div>
        </>
      )}
    </>
  );
}
