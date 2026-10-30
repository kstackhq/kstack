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

import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { Dialog } from './dialog';

// A press as base-ui's outside-press reads one: down, up, then the click.
function press(target: Element) {
  fireEvent.pointerDown(target);
  fireEvent.mouseDown(target);
  fireEvent.pointerUp(target);
  fireEvent.mouseUp(target);
  fireEvent.click(target);
}

function renderDialog() {
  const onOpenChange = vi.fn();
  render(
    <Dialog open onOpenChange={onOpenChange} title="Settings">
      body
    </Dialog>,
  );
  return onOpenChange;
}

const backdrop = () => document.querySelector('[data-slot="dialog-overlay"]')!;

describe('Dialog', () => {
  it('stays open when the window is dragged by the band over the backdrop', async () => {
    const onOpenChange = renderDialog();
    await screen.findByRole('dialog');

    press(screen.getByTestId('dialog-window-drag-region'));

    expect(onOpenChange).not.toHaveBeenCalled();
  });

  it('closes on a press on the backdrop', async () => {
    const onOpenChange = renderDialog();
    await screen.findByRole('dialog');

    press(backdrop());

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it('closes on Escape', async () => {
    const onOpenChange = renderDialog();
    await screen.findByRole('dialog');

    await userEvent.keyboard('{Escape}');

    expect(onOpenChange).toHaveBeenCalledWith(false);
  });
});
