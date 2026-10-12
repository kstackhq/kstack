// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
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
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { useBeforePaint } from './before-paint';

function mount() {
  return renderHook(() => {
    const [value, setValue] = useState(0);
    return { beforePaint: useBeforePaint(), value, setValue };
  });
}

describe('useBeforePaint', () => {
  it('answers one function for the life of the hook', () => {
    const { result, rerender } = mount();
    const first = result.current.beforePaint;
    rerender();
    expect(result.current.beforePaint).toBe(first);
  });

  it('runs what was queued, in order, on the next commit, and never again', () => {
    const { result } = mount();
    const ran: number[] = [];
    act(() => {
      result.current.beforePaint(() => {
        ran.push(1);
      });
      result.current.beforePaint(() => {
        ran.push(2);
      });
      expect(ran).toEqual([]);
    });
    expect(ran).toEqual([1, 2]);

    act(() => result.current.setValue(1));
    expect(ran).toEqual([1, 2]);
  });

  it('runs on the commit queuing it makes, though the update beside it changes nothing', () => {
    const { result } = mount();
    act(() => result.current.setValue(1));
    const ran = vi.fn();
    act(() => {
      result.current.setValue(1);
      result.current.beforePaint(ran);
    });
    expect(ran).toHaveBeenCalledOnce();
  });

  it('settles each promise with its callback', async () => {
    const { result } = mount();
    let finish = () => {};
    let sync!: Promise<void>;
    let async!: Promise<void>;
    const order: string[] = [];
    act(() => {
      sync = result.current.beforePaint(() => {});
      async = result.current.beforePaint(
        () =>
          new Promise<void>((resolve) => {
            finish = resolve;
          }),
      );
    });
    sync.then(() => order.push('sync'));
    async.then(() => order.push('async'));

    await sync;
    expect(order).toEqual(['sync']);

    finish();
    await async;
    expect(order).toEqual(['sync', 'async']);
  });

  it('rejects with what a callback threw, and still runs the rest', async () => {
    const { result } = mount();
    const failure = new Error('layout gone');
    const next = vi.fn();
    let failed!: Promise<void>;
    let ran!: Promise<void>;
    act(() => {
      failed = result.current.beforePaint(() => {
        throw failure;
      });
      ran = result.current.beforePaint(next);
    });

    await expect(failed).rejects.toBe(failure);
    await ran;
    expect(next).toHaveBeenCalledOnce();
  });
});
