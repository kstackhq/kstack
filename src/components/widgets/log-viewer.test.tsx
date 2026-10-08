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

import { createViewerStore } from '@/lib/log-viewer-store';
import { LogViewer } from './log-viewer';

describe('LogViewer', () => {
  it('holds its place until the viewer lands', () => {
    render(
      <LogViewer
        query={{ sources: [], filters: [], grep: '', anchor: { kind: 'Tail', at: null }, pinToEnd: true }}
        openAt={{ kind: 'Tail', at: null }}
        pinToEnd
        store={createViewerStore()}
      />,
    );
    expect(screen.getByText('The log viewer is not built yet.')).toBeInTheDocument();
  });
});
