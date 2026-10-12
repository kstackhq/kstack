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
// @vitest-environment node
import { ESLint } from 'eslint';
import { beforeAll, describe, expect, it } from 'vitest';

import { LOG_VIEWER_IS_A_LIBRARY_MESSAGE, TEMPLATE_ENGINE_MESSAGE } from './eslint.config';

// The config's own invariants, linted against text at paths that exist nowhere.
// The project service refuses a file the TS project does not hold, which would
// fail every case for the wrong reason, so each path is let in by name.

const LIBRARY = 'src/lib/log-viewer/__lint__.ts';
const NESTED = 'src/lib/log-viewer/rows/__lint__.ts';
const APP = 'src/components/__lint__.ts';

const eslint = new ESLint({
  overrideConfigFile: 'eslint.config.ts',
  overrideConfig: {
    languageOptions: { parserOptions: { projectService: { allowDefaultProject: [LIBRARY, NESTED, APP] } } },
  },
});

async function restrictedImports(source: string, filePath: string) {
  const [result] = await eslint.lintText(`import x from '${source}';\n\nexport const y = x;\n`, { filePath });
  return result.messages
    .filter((m) => m.ruleId === 'no-restricted-imports' || m.ruleId === 'kstack/log-viewer-is-a-library')
    .map((m) => m.message);
}

// The first lint builds the TypeScript program, which outlasts a test's budget
// under a full run's load; pay for it once.
beforeAll(async () => {
  await eslint.lintText('', { filePath: LIBRARY });
}, 60_000);

describe('custom/log-viewer-is-a-library', () => {
  it.each(['@/lib/x', '../x', '../../x'])('refuses %s under src/lib/log-viewer/', async (source) => {
    const messages = await restrictedImports(source, LIBRARY);
    expect(messages).toHaveLength(1);
    expect(messages[0]).toContain(LOG_VIEWER_IS_A_LIBRARY_MESSAGE);
  });

  it.each(['../../x', '../../../x'])('refuses %s from a subdirectory of the library', async (source) => {
    const messages = await restrictedImports(source, NESTED);
    expect(messages).toHaveLength(1);
    expect(messages[0]).toContain(LOG_VIEWER_IS_A_LIBRARY_MESSAGE);
  });

  it.each([
    ['./x', LIBRARY],
    ['./x', NESTED],
    ['../x', NESTED],
  ])('lets the library import %s from %s', async (source, filePath) => {
    expect(await restrictedImports(source, filePath)).toEqual([]);
  });

  it('keeps the template-engine ban inside the library', async () => {
    const messages = await restrictedImports('handlebars', LIBRARY);
    expect(messages).toHaveLength(1);
    expect(messages[0]).toContain(TEMPLATE_ENGINE_MESSAGE);
  });

  it.each(['@/lib/x', '../x', '../../x'])('lets the app import %s', async (source) => {
    expect(await restrictedImports(source, APP)).toEqual([]);
  });
});
