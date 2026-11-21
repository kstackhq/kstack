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

import { highlight, load, loaded } from '@/lib/highlight';

describe('highlight', () => {
  it('has the common grammars ready for the first render', () => {
    for (const lang of ['yaml', 'json', 'bash', 'shell', 'go', 'diff', 'dockerfile', 'xml', 'plaintext']) {
      expect(loaded(lang)).toBe(true);
    }
    expect(JSON.stringify(highlight('yaml', 'kind: Pod'))).toContain('hljs-attr');
  });

  it('loads a grammar on demand, then highlights synchronously', async () => {
    expect(loaded('rust')).toBe(false);
    await expect(load('rust')).resolves.toBe(true);
    expect(loaded('rust')).toBe(true);
    const tree = highlight('rust', 'fn main() {}');
    expect(JSON.stringify(tree)).toContain('hljs-keyword');
  });

  it('resolves the aliases a fence is written with', async () => {
    await expect(load('yml')).resolves.toBe(true);
    expect(loaded('yaml')).toBe(true);
    expect(loaded('YML')).toBe(true);
  });

  it('has no grammar for a word it does not know', async () => {
    await expect(load('klingon')).resolves.toBe(false);
    expect(loaded('klingon')).toBe(false);
  });

  it('shares one load between callers asking while it is in flight', async () => {
    const [a, b] = await Promise.all([load('python'), load('python')]);
    expect(a && b).toBe(true);
    expect(loaded('py')).toBe(true);
  });
});
