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
import { useCallback, useLayoutEffect, useRef, useState } from 'react';

export type BeforePaintCallback = () => void | Promise<void>;

/** Queues a callback; the promise settles as the callback does. */
export type BeforePaint = (callback: BeforePaintCallback) => Promise<void>;

type Queued = {
  callback: BeforePaintCallback;
  resolve: () => void;
  reject: (error: unknown) => void;
};

/**
 * Runs queued callbacks after the next commit and before the browser paints it, so
 * a scroll can be set against the DOM that commit made. Queuing schedules that
 * commit itself, so queue a callback in the same task as the update it follows:
 * React batches the two, and the callback runs whatever the update changed.
 */
export function useBeforePaint(): BeforePaint {
  const queueRef = useRef<Queued[]>([]);
  const [queued, setQueued] = useState(0);

  useLayoutEffect(() => {
    const queue = queueRef.current;
    queueRef.current = [];
    queue.forEach(({ callback, resolve, reject }) => {
      try {
        Promise.resolve(callback()).then(resolve, reject);
      } catch (error) {
        reject(error);
      }
    });
  }, [queued]);

  return useCallback(
    (callback) =>
      new Promise<void>((resolve, reject) => {
        queueRef.current.push({ callback, resolve, reject });
        setQueued((n) => n + 1);
      }),
    [],
  );
}
