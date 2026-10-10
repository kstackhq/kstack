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

import { anchorLine, SourceLine } from './logs-view-lines';

describe('anchorLine', () => {
  it('names each edge and the moment as stamped', () => {
    expect(anchorLine({ kind: 'Head', at: null })).toBe('from the start');
    expect(anchorLine({ kind: 'Tail', at: null })).toBe('at the newest line');
    expect(anchorLine({ kind: 'At', at: '2026-10-08T14:02:00Z' })).toBe('from 2026-10-08T14:02:00Z');
  });
});

describe('SourceLine', () => {
  it('names the source, its containers and the previous instance, the names spelled out', () => {
    const { container } = render(
      <SourceLine
        source={{
          namespace: 'prod',
          kind: 'Pod',
          name: 'work‮er-1',
          containers: ['app', 'istio-proxy'],
          previous: true,
        }}
      />,
    );
    expect(container).toHaveTextContent('Pod work');
    expect(container).toHaveTextContent('er-1 in prod (app, istio-proxy), previous instance');
    expect(container.querySelector('mark')).not.toBeNull();
    expect(screen.queryByText('‮')).not.toBeInTheDocument();
  });
});
