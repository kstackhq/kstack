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

import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { expectProseText } from '@/test-utils';

import { MemoryInstructions, SAVE_DELAY_MS, SAVED_SHOWN_MS } from './instructions';

const box = () => screen.getByRole('textbox', { name: 'Instructions' });
// Hidden by opacity, so it is in the tree throughout.
const saved = () => screen.getByText('Saved');
const type = (text: string) => fireEvent.change(box(), { target: { value: text } });
const advance = (ms: number) => {
  act(() => {
    vi.advanceTimersByTime(ms);
  });
};

describe('MemoryInstructions', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('keeps spellcheck but no other suggestions', () => {
    render(<MemoryInstructions />);
    expectProseText(box());
  });

  it('opens empty, with its placeholder and "Saved" hidden', () => {
    render(<MemoryInstructions />);
    expect(box()).toHaveValue('');
    expect(box()).toHaveAttribute('placeholder', 'e.g. keep explanations brief and to the point');
    expect(saved()).toHaveAttribute('aria-hidden', 'true');
    expect(saved()).toHaveClass('opacity-0');
  });

  it('shows "Saved" a pause after the last keystroke, then hides it', () => {
    render(<MemoryInstructions />);
    type('Always use traefik.');
    expect(box()).toHaveValue('Always use traefik.');

    advance(SAVE_DELAY_MS - 1);
    expect(saved()).toHaveAttribute('aria-hidden', 'true');
    advance(1);
    expect(saved()).toHaveAttribute('aria-hidden', 'false');
    expect(saved()).not.toHaveClass('opacity-0');
    expect(screen.getByRole('status')).toBe(saved());

    advance(SAVED_SHOWN_MS);
    expect(saved()).toHaveAttribute('aria-hidden', 'true');
  });

  it('restarts the wait on each keystroke, and hides "Saved" while typing', () => {
    render(<MemoryInstructions />);
    type('Always');
    advance(SAVE_DELAY_MS - 100);
    type('Always use');
    advance(SAVE_DELAY_MS - 100);
    expect(saved()).toHaveAttribute('aria-hidden', 'true');
    advance(100);
    expect(saved()).toHaveAttribute('aria-hidden', 'false');

    type('Always use traefik.');
    expect(saved()).toHaveAttribute('aria-hidden', 'true');
  });

  it('drops its timer when it unmounts', () => {
    const { unmount } = render(<MemoryInstructions />);
    type('Always use traefik.');
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
