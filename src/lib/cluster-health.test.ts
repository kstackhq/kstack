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

import { cacheFailureSummary, connectionFailureSummary, contextHealth, overallTone } from '@/lib/cluster-health';
import { clusterRecord } from '@/test-utils';

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

describe('connectionFailureSummary', () => {
  it('names the probe’s class of failure', () => {
    expect(connectionFailureSummary('Unreachable')).toBe('Can’t reach the server');
    expect(connectionFailureSummary('TLSInvalid')).toBe('Server certificate rejected');
    expect(connectionFailureSummary('Unauthorized')).toBe('Credentials rejected');
  });

  it('reads an unclassified, unknown or missing reason as a bare failure', () => {
    expect(connectionFailureSummary('ProbeFailed')).toBe('Connection failed');
    expect(connectionFailureSummary('constructor')).toBe('Connection failed');
    expect(connectionFailureSummary(undefined)).toBe('Connection failed');
  });
});

describe('cacheFailureSummary', () => {
  it('names a failed sync by its class of failure', () => {
    expect(cacheFailureSummary('SyncFailed', 'Forbidden')).toBe('Permission denied');
    expect(cacheFailureSummary('SyncFailed', 'NotFound')).toBe('Resource no longer served');
    expect(cacheFailureSummary('SyncFailed', 'Timeout')).toBe('Server didn’t answer in time');
  });

  it('names a failed sync with no class, or an unknown one, as a failed sync', () => {
    expect(cacheFailureSummary('SyncFailed')).toBe('Some kinds aren’t syncing');
    expect(cacheFailureSummary('SyncFailed', '')).toBe('Some kinds aren’t syncing');
    expect(cacheFailureSummary('SyncFailed', 'Bogus')).toBe('Some kinds aren’t syncing');
  });

  it('names a failed store, and anything else as a cache error', () => {
    expect(cacheFailureSummary('StoreFailed')).toBe('Cache storage failed');
    expect(cacheFailureSummary(undefined)).toBe('Cache error');
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
      summary: 'Active · Current',
    });
  });

  it('reads a disconnected record as an error, its sync stalled', () => {
    expect(contextHealth([clusterRecord({ context: 'prod', connected: 'False' })], 'prod')).toEqual({
      tone: 'error',
      summary: 'Disconnected · Stalled',
    });
  });
});
