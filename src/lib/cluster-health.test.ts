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

import { describe, expect, it } from 'vitest';

import { clusterRecord } from '@/test-utils';
import { contextHealth, overallTone } from '@/lib/cluster-health';

describe('overallTone', () => {
  it('rolls up to the most severe of the two sub-system tones', () => {
    expect(overallTone('ok', 'ok')).toBe('ok');
    expect(overallTone('ok', 'muted')).toBe('ok');
    expect(overallTone('muted', 'muted')).toBe('muted');
    expect(overallTone('attention', 'muted')).toBe('attention');
    expect(overallTone('ok', 'attention')).toBe('attention');
    expect(overallTone('error', 'attention')).toBe('error');
    expect(overallTone('ok', 'error')).toBe('error');
  });
});

describe('contextHealth', () => {
  it('reads a context with no record as muted and not reached', () => {
    expect(contextHealth([clusterRecord({ context: 'staging' })], 'prod')).toEqual({
      tone: 'muted',
      summary: 'Not reached',
    });
  });

  it('reads every context so before the clusters snapshot lands', () => {
    expect(contextHealth(null, 'prod')).toEqual({ tone: 'muted', summary: 'Not reached' });
  });

  it('reads a connected, watching record as ok', () => {
    expect(contextHealth([clusterRecord({ context: 'prod' })], 'prod')).toEqual({
      tone: 'ok',
      summary: 'Active · Synced',
    });
  });

  it('reads a disconnected record as an error, its sync stalled', () => {
    expect(contextHealth([clusterRecord({ context: 'prod', connected: 'False' })], 'prod')).toEqual({
      tone: 'error',
      summary: 'Disconnected · Stalled',
    });
  });
});
