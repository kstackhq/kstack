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

import { PageHeader } from './page-header';

describe('PageHeader', () => {
  it('draws the title, through VisibleText, as the page heading', () => {
    render(<PageHeader title={'a\u200bb'} />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('a\\u{200B}b');
  });

  it('draws the count when given', () => {
    render(<PageHeader title="Events" count={11} />);
    expect(screen.getByTestId('page-count')).toHaveTextContent('11');
  });

  it('draws no count when it is not known, and a zero when it is', () => {
    const { rerender } = render(<PageHeader title="Pods" />);
    expect(screen.queryByTestId('page-count')).not.toBeInTheDocument();
    rerender(<PageHeader title="Pods" count={0} />);
    expect(screen.getByTestId('page-count')).toHaveTextContent('0');
  });

  it('draws its note under the title', () => {
    render(<PageHeader title="Events">Events in checkout.</PageHeader>);
    expect(screen.getByText('Events in checkout.')).toBeInTheDocument();
  });
});
