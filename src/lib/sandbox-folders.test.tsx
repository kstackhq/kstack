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
import { beforeEach, describe, expect, it, type Mock, vi } from 'vitest';

// The seams: the watch's frames, folded by the hook's own reducer, and each
// mutation by name.
const { useWatchSubscriptionMock, useMutationMock, watch, mutations } = vi.hoisted(() => ({
  useWatchSubscriptionMock: vi.fn(),
  useMutationMock: vi.fn(),
  watch: {} as { frames?: unknown[] },
  mutations: {} as Record<string, Mock<(variables: unknown) => Promise<unknown>>>,
}));
vi.mock('urql', () => ({ useMutation: useMutationMock }));
vi.mock('@/lib/graphql/use-watch-subscription', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/graphql/use-watch-subscription')>()),
  useWatchSubscription: useWatchSubscriptionMock,
}));
// Each document is its name, so the mock can tell the mutations apart.
vi.mock('@/gql', () => ({ graphql: (source: string) => /(?:query|subscription|mutation) (\w+)/.exec(source)![1] }));

const { chatGrantsContext } = await import('./chat-grants');
const { useSandboxFolders } = await import('./sandbox-folders');

const code = { id: 'a', path: '/Users/ren/code', write: false, refused: null };
const gone = { id: 'b', path: '/Users/ren/gone', write: true, refused: 'This folder does not exist.' };
const folders = {
  always: [code],
  chat: [gone],
  never: ['/Users/ren/.ssh'],
  closed: ['/Users/ren/Documents'],
  wide: ['/Users/ren', '/Users'],
  rulesHeld: true,
};

beforeEach(() => {
  vi.clearAllMocks();
  watch.frames = undefined;
  useWatchSubscriptionMock.mockImplementation((_args, reduce) => ({
    data: watch.frames && reduce(undefined, watch.frames),
    connected: true,
  }));
  useMutationMock.mockImplementation((doc: string) => {
    mutations[doc] ??= vi.fn();
    return [{}, mutations[doc]];
  });
});

describe('useSandboxFolders', () => {
  it('is undefined until the watch answers', () => {
    const { result } = renderHook(() => useSandboxFolders('c1'));
    expect(result.current.always).toBeUndefined();
    expect(result.current.chat).toBeUndefined();
    expect(result.current.rulesHeld).toBe(false);
  });

  it('follows the watch: the grants, the never-readable and closed lists and the wide folders', () => {
    const { result, rerender } = renderHook(() => useSandboxFolders('c1'));
    expect(useWatchSubscriptionMock).toHaveBeenCalledWith(
      expect.objectContaining({ variables: { chatID: 'c1' } }),
      expect.any(Function),
      { unshared: true },
    );
    watch.frames = [{ sandboxFoldersWatch: folders }];
    rerender();
    expect(result.current.always).toEqual([code]);
    expect(result.current.chat).toEqual([gone]);
    expect(result.current.never).toEqual(['/Users/ren/.ssh']);
    expect(result.current.closed).toEqual(['/Users/ren/Documents']);
    expect(result.current.wide).toEqual(['/Users/ren', '/Users']);
    expect(result.current.rulesHeld).toBe(true);

    // A grant or Remove made anywhere arrives as the next whole frame.
    watch.frames.push({ sandboxFoldersWatch: { ...folders, always: [], chat: [gone, code] } });
    rerender();
    expect(result.current.always).toEqual([]);
    expect(result.current.chat).toEqual([gone, code]);
  });

  it('watches with no chat when none is named', () => {
    renderHook(() => useSandboxFolders());
    expect(useWatchSubscriptionMock).toHaveBeenCalledWith(
      expect.objectContaining({ variables: { chatID: null } }),
      expect.any(Function),
      { unshared: true },
    );
  });

  it('grants a folder, in flight until it answers', async () => {
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
  });
});
