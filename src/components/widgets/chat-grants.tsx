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

// The composer's "Allowed for this chat" list: the chat's own rules, each in
// the words Settings uses, with Remove, and under a folder grant no run takes
// the reason. It draws nothing while the chat holds none. A rule's line is the
// sidecar's, drawn through VisibleText, since its context and namespace are
// cluster text.
import { useState } from 'react';

import { ShieldCheck } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';

import { VisibleText } from '@/components/widgets/visible-text';
import { useChatGrants } from '@/lib/chat-grants';
import { useSandboxFolders } from '@/lib/sandbox-folders';

export function ChatGrants({ chatID }: { chatID: string }) {
  const { rules, removing, error, remove } = useChatGrants(chatID);
  const { chat } = useSandboxFolders(chatID);
  const [open, setOpen] = useState(false);
  if (!rules || rules.length === 0) return null;
  const refused = new Map(chat?.map((f) => [f.id, f.refused]));

  return (
    <div className="relative shrink-0">
      <Button
        type="button"
        variant="ghost"
        size="xs"
        className="rounded-full text-xs font-normal"
        aria-expanded={open}
        onClick={() => setOpen((was) => !was)}
      >
        <ShieldCheck aria-hidden />
        {rules.length} allowed
      </Button>
      {open && (
        <div className="absolute bottom-full left-0 z-10 mb-1 w-80 rounded-md border bg-popover p-2 text-xs shadow-md">
          <p className="mb-1 text-muted-foreground">Allowed for this chat</p>
          <ul aria-label="Allowed for this chat" className="flex flex-col gap-1">
            {rules.map((rule) => (
              <li key={rule.id} className="flex items-start justify-between gap-2">
                <span className="min-w-0 break-words">
                  <VisibleText text={rule.line} />
                  {refused.get(rule.id) && <span className="block text-muted-foreground">{refused.get(rule.id)}</span>}
                </span>
                <Button
                  type="button"
                  variant="outline"
                  size="xs"
                  disabled={removing === rule.id}
                  onClick={() => remove(rule.id)}
                >
                  Remove
                </Button>
              </li>
            ))}
          </ul>
          {error && (
            <p role="alert" className="mt-1 text-destructive">
              {error}
            </p>
          )}
        </div>
      )}
    </div>
  );
}
