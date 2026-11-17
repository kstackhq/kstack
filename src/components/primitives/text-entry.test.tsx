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

import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { expectCodeText, expectProseText } from '@/test-utils';

import { Input, SearchBox, Textarea } from './text-entry';

describe('text entry', () => {
  it('turns off suggestions, correction and spellcheck by default', () => {
    render(
      <>
        <Input aria-label="input" />
        <Textarea aria-label="textarea" />
      </>,
    );
    expectCodeText(screen.getByLabelText('input'));
    expectCodeText(screen.getByLabelText('textarea'));
  });

  it('keeps spellcheck for prose', () => {
    render(
      <>
        <Input prose aria-label="input" />
        <Textarea prose aria-label="textarea" />
      </>,
    );
    expectProseText(screen.getByLabelText('input'));
    expectProseText(screen.getByLabelText('textarea'));
  });

  it('turns them off in a search box too', () => {
    render(<SearchBox aria-label="search" />);
    expectCodeText(screen.getByRole('searchbox', { name: 'search' }));
  });

  // jsdom applies no CSS, so this checks the class that turns the library's
  // ring off, and that a caller's classes still join it.
  it('marks focus by the border alone, never a ring', () => {
    render(
      <>
        <Input aria-label="input" className="h-8" />
        <Textarea aria-label="textarea" />
        <SearchBox aria-label="search" />
      </>,
    );
    for (const box of [
      screen.getByLabelText('input'),
      screen.getByLabelText('textarea'),
      screen.getByLabelText('search'),
    ]) {
      expect(box).toHaveClass('focus-visible:border-ring', 'focus-visible:ring-0');
      expect(box).not.toHaveClass('focus-visible:ring-3');
    }
    expect(screen.getByLabelText('input')).toHaveClass('h-8');
  });

  it('lets a caller override a setting', () => {
    render(<Input autoComplete="on" aria-label="input" />);
    expect(screen.getByLabelText('input')).toHaveAttribute('autocomplete', 'on');
  });
});
