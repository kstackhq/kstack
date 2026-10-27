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

// A drag from a resize handle's pointerdown to the pointerup that ends it:
// `onMove` gets every pointer position in between.
import type { PointerEvent as ReactPointerEvent } from 'react';

export function dragFrom(e: ReactPointerEvent<HTMLElement>, cursor: string, onMove: (ev: PointerEvent) => void) {
  e.preventDefault();
  // Pointer capture keeps the drag alive when the cursor outruns the thin strip.
  e.currentTarget.setPointerCapture(e.pointerId);
  // Force the cursor for the whole drag (else it reverts off the strip);
  // `user-select: none` stops text selection.
  const { body } = document;
  const prevCursor = body.style.cursor;
  const prevSelect = body.style.userSelect;
  body.style.cursor = cursor;
  body.style.userSelect = 'none';
  const onUp = () => {
    body.style.cursor = prevCursor;
    body.style.userSelect = prevSelect;
    window.removeEventListener('pointermove', onMove);
    window.removeEventListener('pointerup', onUp);
  };
  window.addEventListener('pointermove', onMove);
  window.addEventListener('pointerup', onUp);
}
