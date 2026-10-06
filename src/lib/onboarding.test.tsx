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

const { query, sandbox, openDialog, mutate } = vi.hoisted(() => ({
  query: { current: {} },
  sandbox: { current: {} as { available: boolean | undefined; failed: boolean } },
  openDialog: vi.fn(),
  mutate: vi.fn(),
}));
vi.mock('urql', () => ({
  useQuery: () => [query.current, vi.fn()],
  useMutation: () => [{}, mutate],
}));
vi.mock('@/lib/sandbox', () => ({ useSandbox: () => sandbox.current }));
vi.mock('@/lib/dialog', () => ({ useDialog: () => ({ openDialog }) }));

const { useOnboardingFinish, useOnboardingLaunch } = await import('./onboarding');

const unfinished = { data: { onboarding: { finished: false } } };

beforeEach(() => {
  vi.clearAllMocks();
  query.current = unfinished;
  sandbox.current = { available: true, failed: false };
});

describe('useOnboardingLaunch', () => {
  it('opens the flow once when it is not finished and the sandbox has answered', () => {
    const { rerender } = renderHook(() => useOnboardingLaunch());
    expect(openDialog).toHaveBeenCalledExactlyOnceWith('onboarding');
    rerender();
    expect(openDialog).toHaveBeenCalledOnce();
  });

  it('opens it on a machine with no sandbox too', () => {
    sandbox.current = { available: false, failed: false };
    renderHook(() => useOnboardingLaunch());
    expect(openDialog).toHaveBeenCalledExactlyOnceWith('onboarding');
  });

  // A failure read as "no sandbox" would draw the one screen, whose OK finishes
  // the flow on a machine that has one.
  it('waits for the sandbox to answer', () => {
    sandbox.current = { available: undefined, failed: false };
    const { rerender } = renderHook(() => useOnboardingLaunch());
    expect(openDialog).not.toHaveBeenCalled();

    sandbox.current = { available: true, failed: false };
    rerender();
    expect(openDialog).toHaveBeenCalledOnce();
  });

  it('opens nothing when the flow is finished', () => {
    query.current = { data: { onboarding: { finished: true } } };
    renderHook(() => useOnboardingLaunch());
    expect(openDialog).not.toHaveBeenCalled();
  });

  it('opens nothing when either query fails', () => {
    query.current = { error: new Error('unreachable') };
    renderHook(() => useOnboardingLaunch());
    sandbox.current = { available: undefined, failed: true };
    query.current = unfinished;
    renderHook(() => useOnboardingLaunch());
    expect(openDialog).not.toHaveBeenCalled();
  });
});

describe('useOnboardingFinish', () => {
  it('writes the flag', async () => {
    mutate.mockResolvedValue({ data: { onboardingFinish: { finished: true } } });
    const { result } = renderHook(() => useOnboardingFinish());
    let done = false;
    await act(async () => {
      done = await result.current.finish();
    });
    expect(mutate).toHaveBeenCalledOnce();
    expect(done).toBe(true);
    expect(result.current.finishError).toBeNull();
  });

  it('answers false with the refusal', async () => {
    mutate.mockResolvedValue({ error: { graphQLErrors: [{ message: 'disk full' }], message: 'x' } });
    const { result } = renderHook(() => useOnboardingFinish());
    let done = true;
    await act(async () => {
      done = await result.current.finish();
    });
    expect(done).toBe(false);
    expect(result.current.finishError).toBe('disk full');
    expect(result.current.finishing).toBe(false);
  });
});
