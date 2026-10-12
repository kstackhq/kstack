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
import { afterEach, describe, expect, it, vi } from 'vitest';

import { momentBefore, momentNow } from './moment';

afterEach(() => {
  vi.useRealTimers();
});

describe('momentNow', () => {
  it('is the clock as a bare RFC 3339 time', () => {
    vi.useFakeTimers();
    vi.setSystemTime(Date.parse('2026-10-08T14:00:00.250Z'));
    expect(momentNow()).toBe('2026-10-08T14:00:00.250Z');
  });
});

describe('momentBefore', () => {
  it('steps back by the milliseconds, reading a timestamp to the millisecond', () => {
    expect(momentBefore('2026-10-08T14:00:10.123456789Z', 5000, '2026-10-08T14:00:00Z')).toBe(
      '2026-10-08T14:00:05.123Z',
    );
  });

  it('goes no earlier than the floor', () => {
    expect(momentBefore('2026-10-08T14:00:02Z', 5000, '2026-10-08T14:00:00.5Z')).toBe('2026-10-08T14:00:00.500Z');
  });

  it('throws on a timestamp it cannot read', () => {
    expect(() => momentBefore('soon', 1, '2026-10-08T14:00:00Z')).toThrow(RangeError);
  });
});
