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
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { state, useQueryMock } = vi.hoisted(() => ({ state: { current: {} }, useQueryMock: vi.fn() }));
vi.mock('urql', () => ({ useQuery: useQueryMock }));
vi.mock('@/gql', () => ({ graphql: () => ({}) }));

const { useSandboxCloudAccess } = await import('./sandbox-cloud-access');

beforeEach(() => {
  state.current = { fetching: true };
  useQueryMock.mockImplementation(() => [state.current, vi.fn()]);
});

describe('useSandboxCloudAccess', () => {
  it('is undefined until the sidecar answers', () => {
    expect(renderHook(() => useSandboxCloudAccess()).result.current).toBeUndefined();
  });

  it("answers the platforms sandboxed commands may reach, in the sidecar's order", () => {
    state.current = {
      fetching: false,
      data: {
        sandboxCloudAccess: [
          { target: 'AWS', on: true },
          { target: 'GitHub', on: false },
          { target: 'GCP', on: true },
        ],
      },
    };
    expect(renderHook(() => useSandboxCloudAccess()).result.current).toEqual(['AWS', 'GCP']);
  });
});
