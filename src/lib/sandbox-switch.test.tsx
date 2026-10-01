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

import { act, render, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const { setMock } = vi.hoisted(() => ({ setMock: vi.fn() }));
vi.mock('urql', () => ({ useMutation: () => [{}, setMock] }));
vi.mock('@/gql', () => ({ graphql: () => ({}) }));

const { SandboxSwitchProvider, useSandboxSwitch } = await import('./sandbox-switch');

// The mutation's answer, held until the test lets it go.
function deferred() {
  let resolve!: (result: unknown) => void;
  setMock.mockReturnValueOnce(
    new Promise((r) => {
      resolve = r;
    }),
  );
  return (result: unknown) => act(async () => resolve(result));
}

const committed = { data: { chatSandboxDisabledSet: { id: 'c1', sandboxDisabled: true } } };
const dropped = { error: { networkError: new Error('sidecar unreachable'), graphQLErrors: [] } };

const wrapper = ({ children }: { children: ReactNode }) => <SandboxSwitchProvider>{children}</SandboxSwitchProvider>;

beforeEach(() => {
  vi.clearAllMocks();
});

describe('useSandboxSwitch', () => {
  const renderSwitch = (watched: boolean | undefined) =>
    renderHook(({ outside }) => useSandboxSwitch('c1', outside), { wrapper, initialProps: { outside: watched } });

  it("sets the chat's switch, and is switching until the watch shows what it committed", async () => {
    const answer = deferred();
    const { result, rerender } = renderSwitch(false);
    expect(result.current.switching).toBe(false);

    act(() => {
      result.current.setSandboxDisabled(true);
    });
    expect(setMock).toHaveBeenCalledWith({ id: 'c1', sandboxDisabled: true });
    expect(result.current.switching).toBe(true);

    // The mutation's answer comes before the watch's frame: a send now would pass
    // the old value.
    await answer(committed);
    expect(result.current.switching).toBe(true);

    rerender({ outside: true });
    expect(result.current.switching).toBe(false);

    // Settled: a later switch from another window is not this one's to wait on.
    rerender({ outside: false });
    expect(result.current.switching).toBe(false);
  });

  it('stops switching once the mutation answers when the watch got there first', async () => {
    const answer = deferred();
    const { result, rerender } = renderSwitch(false);
    act(() => {
      result.current.setSandboxDisabled(true);
    });
    rerender({ outside: true });
    expect(result.current.switching).toBe(true);

    await answer(committed);
    expect(result.current.switching).toBe(false);
  });

  it('stops switching when the mutation fails', async () => {
    const answer = deferred();
    const { result } = renderSwitch(false);
    act(() => {
      result.current.setSandboxDisabled(true);
    });

    await answer(dropped);
    expect(result.current.switching).toBe(false);
  });

  // The pane remounts on navigation, and the mutation it started keeps going.
  it('is still switching in a pane that remounts meanwhile', async () => {
    const answer = deferred();
    const seen: boolean[] = [];
    function Pane({ outside }: { outside: boolean }) {
      const { switching, setSandboxDisabled } = useSandboxSwitch('c1', outside);
      seen.push(switching);
      return (
        <button type="button" onClick={() => setSandboxDisabled(true)}>
          switch
        </button>
      );
    }
    const view = render(
      <SandboxSwitchProvider>
        <Pane key="a" outside={false} />
      </SandboxSwitchProvider>,
    );
    act(() => view.getByRole('button').click());

    view.rerender(
      <SandboxSwitchProvider>
        <Pane key="b" outside={false} />
      </SandboxSwitchProvider>,
    );
    expect(seen.at(-1)).toBe(true);

    await answer(committed);
    expect(seen.at(-1)).toBe(true);

    view.rerender(
      <SandboxSwitchProvider>
        <Pane key="b" outside />
      </SandboxSwitchProvider>,
    );
    expect(seen.at(-1)).toBe(false);
  });

  // A pane that mounts after the answer opens its own watch, whose snapshot
  // already holds the commit.
  it('leaves a committed switch behind with the pane that started it', async () => {
    const answer = deferred();
    const seen: boolean[] = [];
    function Pane({ outside }: { outside: boolean }) {
      const { switching, setSandboxDisabled } = useSandboxSwitch('c1', outside);
      seen.push(switching);
      return (
        <button type="button" onClick={() => setSandboxDisabled(true)}>
          switch
        </button>
      );
    }
    const view = render(
      <SandboxSwitchProvider>
        <Pane key="a" outside={false} />
      </SandboxSwitchProvider>,
    );
    act(() => view.getByRole('button').click());
    await answer(committed);

    view.rerender(
      <SandboxSwitchProvider>
        <Pane key="b" outside={false} />
      </SandboxSwitchProvider>,
    );
    expect(seen.at(-1)).toBe(false);
  });

  it("leaves another chat's switch alone", () => {
    deferred();
    const { result } = renderHook(() => ({ one: useSandboxSwitch('c1', false), two: useSandboxSwitch('c2', false) }), {
      wrapper,
    });
    act(() => {
      result.current.one.setSandboxDisabled(true);
    });
    expect(result.current.one.switching).toBe(true);
    expect(result.current.two.switching).toBe(false);
  });
});
