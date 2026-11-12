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

  it('draws the count and the kind’s names when given', () => {
    render(<PageHeader title="Pods" count={11} meta="v1 · po" />);
    expect(screen.getByTestId('page-count')).toHaveTextContent('11');
    expect(screen.getByText('v1 · po')).toBeInTheDocument();
  });

  it('draws the kind’s names through VisibleText', () => {
    render(<PageHeader title="Pods" meta={'v1\u202e · po'} />);
    expect(screen.getByText(/^v1/)).toHaveTextContent('v1\\u{202E} · po');
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
