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

import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { useConnectors } from './connectors';

describe('useConnectors', () => {
  it('serves the connected services, then the available ones', () => {
    const { result } = renderHook(() => useConnectors());

    expect(result.current).toEqual([
      { id: 'github', name: 'GitHub', detail: 'Signed in as octo-dev', connected: true },
      {
        id: 'claude',
        name: 'Claude',
        detail: 'Signed in as dev@acme.io',
        connected: true,
        manageLabel: 'Manage on Claude',
      },
      { id: 'aws', name: 'AWS', detail: 'Profile prod-admin · eu-west-1', connected: true },
      { id: 'google', name: 'Google', detail: 'Gemini models', connected: false },
      { id: 'chatgpt', name: 'ChatGPT', detail: 'OpenAI models', connected: false },
    ]);
  });
});
