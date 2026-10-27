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

import { useState } from 'react';

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';

import { expectProseText } from '@/test-utils';

import { MemoryInstructions } from './instructions';

// The saved text is the parent's; this gives it somewhere to live.
function Instructions({ initial = '' }: { initial?: string }) {
  const [text, setText] = useState(initial);
  return <MemoryInstructions text={text} onSave={setText} />;
}

const button = (name: string) => screen.getByRole('button', { name });

describe('MemoryInstructions', () => {
  it('keeps spellcheck in the editor but no other suggestions', async () => {
    const user = userEvent.setup();
    render(<Instructions />);
    await user.click(button('Add instructions'));
    expectProseText(screen.getByRole('textbox'));
  });

  it('starts empty, with a prompt to add instructions', () => {
    render(<Instructions />);
    expect(screen.getByText('No instructions added')).toBeInTheDocument();
    expect(button('Add instructions')).toBeInTheDocument();
  });

  it('saves a draft typed into the editor', async () => {
    const user = userEvent.setup();
    render(<Instructions />);

    await user.click(button('Add instructions'));
    await user.type(screen.getByRole('textbox'), 'Always use traefik.');
    await user.click(button('Save'));

    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByText('Always use traefik.')).toBeInTheDocument();
  });

  it('reopens the editor pre-filled on Edit', async () => {
    const user = userEvent.setup();
    render(<Instructions initial="Always use traefik." />);

    await user.click(button('Edit'));
    expect(screen.getByRole('textbox')).toHaveValue('Always use traefik.');
  });

  it('discards the draft on Cancel', async () => {
    const user = userEvent.setup();
    render(<Instructions />);

    await user.click(button('Add instructions'));
    await user.type(screen.getByRole('textbox'), 'Never mind this.');
    await user.click(button('Cancel'));

    expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
    expect(screen.getByText('No instructions added')).toBeInTheDocument();
  });

  it('Clear text empties the draft alone, until Save commits it', async () => {
    const user = userEvent.setup();
    render(<Instructions initial="Always use traefik." />);

    await user.click(button('Edit'));
    await user.click(button('Clear text'));
    expect(screen.getByRole('textbox')).toHaveValue('');

    await user.click(button('Cancel'));
    expect(screen.getByText('Always use traefik.')).toBeInTheDocument();

    await user.click(button('Edit'));
    await user.click(button('Clear text'));
    await user.click(button('Save'));
    expect(screen.getByText('No instructions added')).toBeInTheDocument();
  });

  it('disables Save on an unchanged empty draft, and Clear text once already empty', async () => {
    const user = userEvent.setup();
    render(<Instructions />);

    await user.click(button('Add instructions'));
    expect(button('Save')).toBeDisabled();
    expect(button('Clear text')).toBeDisabled();

    await user.type(screen.getByRole('textbox'), 'Always use traefik.');
    expect(button('Save')).toBeEnabled();
    expect(button('Clear text')).toBeEnabled();
  });
});
