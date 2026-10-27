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

import { Input, Textarea } from './text-entry';

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

  it('lets a caller override a setting', () => {
    render(<Input autoComplete="on" aria-label="input" />);
    expect(screen.getByLabelText('input')).toHaveAttribute('autocomplete', 'on');
  });
});
