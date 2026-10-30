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

import { render, screen, waitFor } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { Markdown } from '@/components/widgets/markdown';

describe('Markdown', () => {
  it('renders markdown as elements', () => {
    render(<Markdown text={'# Pods\n\n- one\n- two\n\n`kubectl`'} />);
    expect(screen.getByRole('heading', { name: 'Pods' })).toBeInTheDocument();
    expect(screen.getAllByRole('listitem')).toHaveLength(2);
    expect(screen.getByText('kubectl').tagName).toBe('CODE');
  });

  it('renders every heading level at its own level', () => {
    render(<Markdown text={'# one\n\n## two\n\n### three\n\n#### four\n\n##### five\n\n###### six'} />);
    ['one', 'two', 'three', 'four', 'five', 'six'].forEach((name, i) => {
      expect(screen.getByRole('heading', { name, level: i + 1 })).toBeInTheDocument();
    });
  });

  it('renders numbered lists, quotes and rules as their elements', () => {
    const { container } = render(<Markdown text={'1. first\n2. second\n\n> quoted\n\n---'} />);
    expect(screen.getByRole('list').tagName).toBe('OL');
    expect(screen.getByText('quoted').closest('blockquote')).toBeInTheDocument();
    expect(container.querySelector('hr')).toBeInTheDocument();
  });

  // Emphasis needs no handler: the browser's default styles draw all three.
  it('renders emphasis as its elements', () => {
    render(<Markdown text="**bold** _italic_ ~~gone~~" />);
    expect(screen.getByText('bold').tagName).toBe('STRONG');
    expect(screen.getByText('italic').tagName).toBe('EM');
    expect(screen.getByText('gone').tagName).toBe('DEL');
  });

  it('renders GitHub tables', () => {
    render(<Markdown text={'| Name | Ready |\n| --- | --- |\n| api | 1/1 |'} />);
    expect(screen.getByRole('columnheader', { name: 'Ready' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '1/1' })).toBeInTheDocument();
  });

  // The text is the model's, and the model has read the cluster.
  it('renders raw HTML as the literal characters', () => {
    render(<Markdown text='before <img src=x onerror="alert(1)"> <b>bold</b> after' />);
    expect(document.querySelector('img, b')).toBeNull();
    expect(screen.getByText(/<b>bold<\/b>/)).toBeInTheDocument();
  });

  it('draws a link without an href to follow', () => {
    render(<Markdown text="see [the docs](https://example.com/x)" />);
    expect(screen.queryByRole('link')).toBeNull();
    expect(screen.getByTitle('https://example.com/x')).toHaveTextContent('the docs');
    expect(document.querySelector('a')).toBeNull();
  });

  // The grammar is a chunk of its own, so the block is plain until it arrives.
  it('highlights a fence that names a language once its grammar arrives', async () => {
    render(<Markdown text={'```yaml\nkind: Pod\n```'} />);
    await waitFor(() => expect(document.querySelector('pre .hljs-attr')).toHaveTextContent('kind'));
  });

  it('leaves a fence naming a language it has no grammar for plain', async () => {
    render(<Markdown text={'```klingon\nkind: Pod\n```'} />);
    await waitFor(() => expect(screen.getByText('kind: Pod').closest('pre')).not.toBeNull());
    expect(document.querySelector('pre [class*="hljs-"]')).toBeNull();
  });

  // A fence naming nothing stays plain rather than being guessed at: a guess is
  // wrong often enough on a short snippet to read as noise.
  it('leaves a fence with no language plain', () => {
    render(<Markdown text={'```\nkind: Pod\n```'} />);
    expect(document.querySelector('pre [class*="hljs-"]')).toBeNull();
    expect(screen.getByText('kind: Pod').closest('pre')).not.toBeNull();
  });

  it('renders an unterminated fence mid-stream as a code block', () => {
    render(<Markdown text={'run:\n\n```sh\nkubectl get pods'} />);
    expect(screen.getByText('kubectl get pods').closest('pre')).not.toBeNull();
  });
});
