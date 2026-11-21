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

// A popover hung from the omnibox (the app bar's cluster chip) rather than from the
// control that opens it, so it starts at the chip's left edge. The library's
// `PopoverContent` anchors on the trigger alone, so the positioner is base-ui's own.
// The chip is narrow; each popup sets a width of its own (`OMNIBOX_POPUP_WIDTH`).
import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { Popover, PopoverTrigger } from '@kubetail/ui/elements/popover';
import type { ReactElement, RefObject } from 'react';

// 640px, less the window's margins on a narrow one.
export const OMNIBOX_POPUP_WIDTH = 'min(640px, calc(100vw - 16px))';

export function OmniboxPopover({
  anchorRef,
  children,
  popup,
  open,
  onOpenChange,
}: {
  anchorRef: RefObject<HTMLElement | null>;
  children: ReactElement;
  popup: ReactElement;
  // Leave both out for a popover that opens and closes on its own.
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger render={children} />
      <PopoverPrimitive.Portal>
        {/* z-50, as the library's popover content has: above the sidebar drawer
            and its scrim. It never flips above the chip: its height is capped
            at the room below, which would otherwise trip the flip. */}
        <PopoverPrimitive.Positioner
          anchor={anchorRef}
          align="start"
          side="bottom"
          sideOffset={8}
          collisionAvoidance={{ side: 'none' }}
          className="z-50"
        >
          {popup}
        </PopoverPrimitive.Positioner>
      </PopoverPrimitive.Portal>
    </Popover>
  );
}
