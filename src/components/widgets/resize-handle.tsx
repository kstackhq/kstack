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

// A pane's drag handle: a thin strip on the edge it resizes from, reporting the
// width the pointer implies. The caller clamps and persists it.
//
// `edge` is which side of the pane the handle rides, and decides the arithmetic:
// a pane grown from its right edge is as wide as the pointer is from its left
// edge (the handle's parent), one anchored at the window's right as wide as
// what is left of the window. `offset` adds any padding between the pane's box
// and the border being dragged.
import type { PointerEvent as ReactPointerEvent } from 'react';
import { useCallback } from 'react';

import { cn } from '@kubetail/ui/lib/utils';

import { dragFrom } from '@/lib/pointer-drag';

type ResizeHandleProps = {
  edge: 'left' | 'right';
  onResize: (px: number) => void;
  offset?: number;
  /** Names the handle for the keyboard and for tests. */
  label: string;
};

export function ResizeHandle({ edge, onResize, offset = 0, label }: ResizeHandleProps) {
  const handlePointerDown = useCallback(
    (e: ReactPointerEvent<HTMLDivElement>) => {
      const left = e.currentTarget.parentElement?.getBoundingClientRect().left ?? 0;
      dragFrom(e, 'col-resize', (ev) => {
        onResize(edge === 'right' ? ev.clientX - left + offset : window.innerWidth - ev.clientX + offset);
      });
    },
    [edge, offset, onResize],
  );

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      onPointerDown={handlePointerDown}
      className={cn('absolute inset-y-0 z-20 w-2 cursor-col-resize touch-none', edge === 'left' && 'left-0')}
      // A right-edge strip straddles the border, half of its 8px either side.
      style={edge === 'right' ? { right: offset - 4 } : undefined}
    />
  );
}
