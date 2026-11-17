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

import { Popover as PopoverPrimitive } from '@base-ui/react/popover';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { OmniboxPopover } from './omnibox-popover';

function renderPopover() {
  return render(
    <div data-testid="host">
      <OmniboxPopover anchorRef={{ current: null }} popup={<PopoverPrimitive.Popup>Popup body</PopoverPrimitive.Popup>}>
        <button type="button">Open</button>
      </OmniboxPopover>
    </div>,
  );
}

describe('OmniboxPopover', () => {
  it('opens on the trigger’s click, in a portal above the sidebar drawer', async () => {
    const user = userEvent.setup();
    renderPopover();
    expect(screen.queryByText('Popup body')).not.toBeInTheDocument();

    await user.click(screen.getByRole('button', { name: 'Open' }));
    const body = await screen.findByText('Popup body');
    expect(screen.getByTestId('host').contains(body)).toBe(false);
    expect(body.closest('.z-50')).not.toBeNull();
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    renderPopover();
    await user.click(screen.getByRole('button', { name: 'Open' }));
    await screen.findByText('Popup body');

    await user.keyboard('{Escape}');
    expect(screen.queryByText('Popup body')).not.toBeInTheDocument();
  });
});
