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

const { chatGrantsContext } = await import('./chat-grants');
const { useSandboxFolders } = await import('./sandbox-folders');

const code = { id: 'a', path: '/Users/ren/code', write: false, refused: null };
const gone = { id: 'b', path: '/Users/ren/gone', write: true, refused: 'This folder does not exist.' };
const queried = {
  fetching: false,
  data: {
    sandboxFolders: {
      always: [code],
      chat: [gone],
      never: ['/Users/ren/.ssh'],
      wide: ['/Users/ren', '/Users'],
      rulesHeld: true,
    },
  },
};

beforeEach(() => {
  vi.clearAllMocks();
  useQueryMock.mockImplementation(() => [queried, reexecute]);
  useMutationMock.mockImplementation((doc: string) => {
    mutations[doc] ??= vi.fn();
    return [{}, mutations[doc]];
  });
});

describe('useSandboxFolders', () => {
  it('reads the grants, the never-readable list and the wide folders', () => {
    const { result } = renderHook(() => useSandboxFolders('c1'));
    expect(useQueryMock).toHaveBeenCalledWith(
      expect.objectContaining({ variables: { chatID: 'c1' }, requestPolicy: 'cache-and-network' }),
    );
    expect(result.current.always).toEqual([code]);
    expect(result.current.chat).toEqual([gone]);
    expect(result.current.never).toEqual(['/Users/ren/.ssh']);
    expect(result.current.wide).toEqual(['/Users/ren', '/Users']);
    expect(result.current.rulesHeld).toBe(true);
  });

  it('asks with no chat when none is named', () => {
    renderHook(() => useSandboxFolders());
    expect(useQueryMock).toHaveBeenCalledWith(expect.objectContaining({ variables: { chatID: null } }));
  });

  it('grants a folder, in flight until it answers, then asks again', async () => {
    let answer!: (value: unknown) => void;
    mutations.FolderGrant.mockImplementation(
      () =>
        new Promise((resolve) => {
          answer = resolve;
        }),
    );
    const { result } = renderHook(() => useSandboxFolders());

    let granted!: Promise<boolean>;
    act(() => {
      granted = result.current.grant('/Users/ren/code', true, 'Always');
    });
    expect(mutations.FolderGrant).toHaveBeenCalledWith(
      { chatID: null, path: '/Users/ren/code', write: true, duration: 'Always' },
      chatGrantsContext,
    );
    expect(result.current.granting).toBe(true);

    await act(async () => answer({ data: { folderGrant: {} } }));
    await expect(granted).resolves.toBe(true);
    expect(result.current.granting).toBe(false);
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });

  // A chat grant is a chat rule, so the chat's list of them is asked again.
  it("names the chat on a grant for it, under the chat rules' context", async () => {
    mutations.FolderGrant.mockResolvedValue({ data: { folderGrant: {} } });
    const { result } = renderHook(() => useSandboxFolders('c1'));
    await act(async () => {
      await result.current.grant('/Users/ren/code', false, 'Chat');
    });
    expect(mutations.FolderGrant).toHaveBeenCalledWith(
      expect.objectContaining({ chatID: 'c1', duration: 'Chat' }),
      chatGrantsContext,
    );
  });

  it("keeps a refused grant's reason, and a link's target", async () => {
    mutations.FolderGrant.mockResolvedValue({
      error: {
        graphQLErrors: [
          {
            message: '/Users/ren/src is a link to /Users/ren/code; grant /Users/ren/code instead.',
            extensions: { code: 'KSTACK_VALIDATION_ERROR', rule: 'link', target: '/Users/ren/code' },
          },
        ],
      },
    });
    const { result } = renderHook(() => useSandboxFolders());
    let granted!: boolean;
    await act(async () => {
      granted = await result.current.grant('/Users/ren/src', false, 'Always');
    });
    expect(granted).toBe(false);
    expect(result.current.grantError).toEqual({
      message: '/Users/ren/src is a link to /Users/ren/code; grant /Users/ren/code instead.',
      target: '/Users/ren/code',
    });

    mutations.FolderGrant.mockResolvedValue({
      error: { graphQLErrors: [{ message: 'Your home can be granted read-only.', extensions: { rule: 'home' } }] },
    });
    await act(async () => {
      await result.current.grant('/Users/ren', true, 'Always');
    });
    expect(result.current.grantError).toEqual({ message: 'Your home can be granted read-only.', target: null });
  });

  it('revokes a grant, in flight by its id, and keeps a refusal', async () => {
    mutations.FolderRevoke.mockResolvedValue({ error: { graphQLErrors: [{ message: 'Record not found' }] } });
    const { result } = renderHook(() => useSandboxFolders());
    await act(async () => result.current.revoke('a'));
    expect(mutations.FolderRevoke).toHaveBeenCalledWith({ id: 'a' });
    expect(result.current.revokeError).toBe('Record not found');
    expect(result.current.revoking).toEqual(new Set());
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });

  it('asks again whenever the window takes focus', () => {
    renderHook(() => useSandboxFolders());
    window.dispatchEvent(new Event('focus'));
    expect(reexecute).toHaveBeenCalledWith({ requestPolicy: 'network-only' });
  });
});
