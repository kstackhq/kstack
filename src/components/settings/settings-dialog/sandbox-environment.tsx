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

// The Sandbox pane's Environment: what a sandboxed command's environment holds,
// and what never passes.
// Placeholder: awaits a query of the run's environment table and NeverEnv, and an import mutation.
import { Button } from '@kubetail/ui/elements/button';

import { VisibleText } from '@/components/primitives/visible-text';

import { Group, InfoPopover, NoteRow } from './group';

const FROM_SHELL = ['PATH', 'LANG', 'TZ'];
const SET_BY_KSTACK = ['HOME', 'TMPDIR', 'KUBECONFIG', 'KUBECACHEDIR', 'TERM=dumb'];
const NEVER_PASSED = [
  'SSH_AUTH_SOCK',
  'GITHUB_TOKEN',
  'GH_TOKEN',
  'GITHUB_ENTERPRISE_TOKEN',
  'GH_ENTERPRISE_TOKEN',
  'DOCKER_HOST',
  'AWS_*',
  'LD_PRELOAD',
  'LD_LIBRARY_PATH',
  'LD_AUDIT',
  'DYLD_INSERT_LIBRARIES',
  'DYLD_LIBRARY_PATH',
  'DYLD_FRAMEWORK_PATH',
  'BASH_ENV',
  'ENV',
  'PROMPT_COMMAND',
];

function Names({ names }: { names: string[] }) {
  return (
    <span className="flex flex-wrap gap-x-3 font-mono text-xs">
      {names.map((name) => (
        <span key={name}>
          <VisibleText text={name} trailing="end" />
        </span>
      ))}
    </span>
  );
}

export function SandboxEnvironment() {
  return (
    <Group
      title="Environment"
      lede="What a sandboxed command's environment holds, and what never passes."
      info={
        <InfoPopover label="Never passed">
          <p className="text-muted-foreground">Never passed, even when a command sets it.</p>
          <ul aria-label="Never passed" className="font-mono">
            {NEVER_PASSED.map((name) => (
              <li key={name}>{name}</li>
            ))}
          </ul>
        </InfoPopover>
      }
      action={
        <Button variant="outline" size="sm" disabled>
          Import from shell
        </Button>
      }
    >
      <ul className="divide-y rounded-lg border">
        <NoteRow label="From your shell">
          <Names names={FROM_SHELL} />
        </NoteRow>
        <NoteRow label="Set by Kstack">
          <Names names={SET_BY_KSTACK} />
        </NoteRow>
      </ul>
      <p className="text-xs text-muted-foreground">Coming soon</p>
    </Group>
  );
}
