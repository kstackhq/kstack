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

// Drag handles for a popup centered under its anchor and hanging from its top
// (the handles' parent). A side grows both sides at once, so the width is twice
// the pointer's distance from the centre; the bottom sets the height; a bottom
// corner sets both. The caller clamps and persists them.
import type { PointerEvent as ReactPointerEvent } from 'react';

import { dragFrom } from '@/lib/pointer-drag';

type Handle = {
  name: string;
  sets: { width?: true; height?: true };
  cursor: string;
  className: string;
};

// Each strip straddles its border, half outside the popup. A corner sits over
// the two edges it shares.
const HANDLES: Handle[] = [
  { name: 'left edge', sets: { width: true }, cursor: 'ew-resize', className: 'inset-y-0 -left-1 z-20 w-2' },
  { name: 'right edge', sets: { width: true }, cursor: 'ew-resize', className: 'inset-y-0 -right-1 z-20 w-2' },
  { name: 'bottom edge', sets: { height: true }, cursor: 'ns-resize', className: 'inset-x-0 -bottom-1 z-20 h-2' },
  {
    name: 'bottom-left corner',
    sets: { width: true, height: true },
    cursor: 'nesw-resize',
    className: '-bottom-1 -left-1 z-30 size-3',
  },
  {
    name: 'bottom-right corner',
    sets: { width: true, height: true },
    cursor: 'nwse-resize',
    className: '-right-1 -bottom-1 z-30 size-3',
  },
];

type PopupResizeHandlesProps = {
  onWidth: (px: number) => void;
  onHeight: (px: number) => void;
  /** Names the handles for the keyboard and for tests: "<label>, left edge". */
  label: string;
};

export function PopupResizeHandles({ onWidth, onHeight, label }: PopupResizeHandlesProps) {
  const start =
    ({ sets, cursor }: Handle) =>
    (e: ReactPointerEvent<HTMLDivElement>) => {
      const box = e.currentTarget.parentElement?.getBoundingClientRect() ?? new DOMRect();
      const centre = box.left + box.width / 2;
      dragFrom(e, cursor, (ev) => {
        if (sets.width) onWidth(2 * Math.abs(ev.clientX - centre));
        if (sets.height) onHeight(ev.clientY - box.top);
      });
    };

  return HANDLES.map((handle) => (
    <div
      key={handle.name}
      role="separator"
      aria-label={`${label}, ${handle.name}`}
      onPointerDown={start(handle)}
      className={`absolute touch-none ${handle.className}`}
      style={{ cursor: handle.cursor }}
    />
  ));
}
