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

// The Sandbox pane's PATH: the frozen list sandboxed commands search, as one
// PATH string. Folders switches each entry; this card only shows the order.
import { Button } from '@kubetail/ui/elements/button';

import { VisibleText } from '@/components/primitives/visible-text';
import type { SandboxPath } from '@/lib/sandbox-path';

import { Group } from './group';

// What commands search while the shell cannot be read: the stored list, or
// with none, the default only before the shell was ever read.
function inEffect(entries: number, resolved: boolean | undefined): string {
  if (entries > 0) return 'The list is from the last time it could.';
  if (resolved) return 'Sandboxed commands search no folder.';
  return "Sandboxed commands use the system's default PATH.";
}

export function SandboxPathSection({ path, onRefreshed }: { path: SandboxPath; onRefreshed: () => void }) {
  const { entries, fault, resolved, shell, importedAt, refreshing, refreshError, refresh } = path;
  return (
    <Group
      title="PATH"
      lede="Your shell's PATH, as last imported. Folders shows which of these a sandboxed command can read."
      action={
        <Button
          variant="outline"
          size="sm"
          disabled={refreshing}
          onClick={async () => {
            // A new PATH can change which binary each executable resolves to.
            if (await refresh()) onRefreshed();
          }}
        >
          Import from shell
        </Button>
      }
    >
      <div className="flex flex-col gap-1 rounded-lg border px-4 py-3">
        <p className="font-mono text-sm break-all whitespace-pre-wrap" data-testid="path">
          <VisibleText text={entries?.map((entry) => entry.dir).join(':') ?? ''} trailing="end" />
        </p>
        {shell && importedAt && (
          <p className="text-xs text-muted-foreground">
            Imported from <VisibleText text={shell} /> on {new Date(importedAt).toLocaleString()}.
          </p>
        )}
        {fault && (
          <p className="text-xs text-muted-foreground">
            {`Kstack could not read your shell's PATH: ${fault}. ${inEffect(entries?.length ?? 0, resolved)}`}
          </p>
        )}
      </div>
      {refreshError && <p className="text-xs text-destructive">{refreshError}</p>}
    </Group>
  );
}
