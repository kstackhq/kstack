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

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { useQueryMock, useMutationMock, reexecute, mutations } = vi.hoisted(() => ({
  useQueryMock: vi.fn(),
  useMutationMock: vi.fn(),
  reexecute: vi.fn(),
  mutations: {} as Record<string, ReturnType<typeof vi.fn>>,
}));
vi.mock('urql', () => ({ useQuery: useQueryMock, useMutation: useMutationMock }));
// Each document is its name, so the mock can tell the mutations apart.
vi.mock('@/gql', () => ({ graphql: (source: string) => /(?:query|mutation) (\w+)/.exec(source)![1] }));

const { useSandboxPath } = await import('./sandbox-path');

const entry = (dir: string, state: string) => ({ dir, target: dir, state, source: 'Shell', shared: false });
const listed = [entry('/opt/homebrew/bin', 'Adopted'), entry('/Users/ren/scripts', 'Pending')];

const queried = {
  fetching: false,
  data: { sandboxPath: listed, sandboxPathFault: 'timeout', sandboxPathResolved: true },
};

beforeEach(() => {
  vi.clearAllMocks();
  useQueryMock.mockImplementation(() => [queried, reexecute]);
  useMutationMock.mockImplementation((doc: string) => {
    mutations[doc] ??= vi.fn();
    return [{}, mutations[doc]];
  });
});

describe('useSandboxPath', () => {
  it('reads the list and the fault', () => {
    const { result } = renderHook(() => useSandboxPath());
    expect(result.current.entries).toEqual(listed);
    expect(result.current.fault).toBe('timeout');
    expect(result.current.resolved).toBe(true);
  });

  it('includes an entry, in flight until it answers, then asks for the list again', async () => {
    let answer!: (value: unknown) => void;
    mutations.SandboxPathInclude.mockImplementation(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    const { result } = renderHook(() => useSandboxPath());

    act(() => {
      result.current.include('/Users/ren/scripts', '/Users/ren/scripts');
    });
    expect(mutations.SandboxPathInclude).toHaveBeenCalledWith({
      dir: '/Users/ren/scripts',
      target: '/Users/ren/scripts',
    });
    expect(result.current.changing).toEqual(new Set(['/Users/ren/scripts']));

    await act(async () => answer({ data: { sandboxPathInclude: [] } }));
    expect(result.current.changing).toEqual(new Set());
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });

  it("keeps a refused change's message", async () => {
    mutations.SandboxPathRemove.mockResolvedValue({
      error: { graphQLErrors: [{ message: 'That folder is already removed.' }] },
    });
    const { result } = renderHook(() => useSandboxPath());
    await act(async () => result.current.remove('/opt/homebrew/bin'));
    expect(mutations.SandboxPathRemove).toHaveBeenCalledWith({ dir: '/opt/homebrew/bin' });
    expect(result.current.changeError).toBe('That folder is already removed.');
    expect(result.current.changing).toEqual(new Set());
  });

  it('settles overlapping changes each for its own dir', async () => {
    let included!: (value: unknown) => void;
    let removed!: (value: unknown) => void;
    mutations.SandboxPathInclude.mockImplementation(
      () =>
        new Promise((resolve) => {
          included = resolve;
        }),
    );
    mutations.SandboxPathRemove.mockImplementation(
      () =>
        new Promise((resolve) => {
          removed = resolve;
        }),
    );
    const { result } = renderHook(() => useSandboxPath());

    act(() => {
      result.current.remove('/opt/homebrew/bin');
      result.current.include('/Users/ren/scripts', '/Users/ren/scripts');
    });
    expect(result.current.changing).toEqual(new Set(['/opt/homebrew/bin', '/Users/ren/scripts']));

    await act(async () => removed({ error: { graphQLErrors: [{ message: 'That folder is already removed.' }] } }));
    expect(result.current.changing).toEqual(new Set(['/Users/ren/scripts']));

    // The include that succeeds after it leaves the refusal on screen.
    await act(async () => included({ data: { sandboxPathInclude: [] } }));
    expect(result.current.changing).toEqual(new Set());
    expect(result.current.changeError).toBe('That folder is already removed.');
  });

  // The fault is the last read's, so a refresh reads it again whatever the answer.
  it("reads the fault again after a refresh, and keeps a refused one's message", async () => {
    mutations.SandboxPathRefresh.mockResolvedValue({
      error: { graphQLErrors: [{ message: 'Your shell did not answer: timeout.' }] },
    });
    const { result } = renderHook(() => useSandboxPath());
    await act(async () => result.current.refresh());
    expect(result.current.refreshError).toBe('Your shell did not answer: timeout.');
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });

    mutations.SandboxPathRefresh.mockResolvedValue({ data: { sandboxPathRefresh: listed } });
    await act(async () => result.current.refresh());
    expect(result.current.refreshError).toBeNull();
    expect(result.current.refreshing).toBe(false);
    expect(reexecute).toHaveBeenCalledTimes(2);
  });

  // Another window can change the list, and each window caches its own, so
  // the list is asked again on opening and whenever the window takes focus.
  it('asks again on opening and on focus', () => {
    renderHook(() => useSandboxPath());
    expect(useQueryMock).toHaveBeenCalledWith(expect.objectContaining({ requestPolicy: 'cache-and-network' }));

    window.dispatchEvent(new Event('focus'));
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });
});
