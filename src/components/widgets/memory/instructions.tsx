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

// A cluster's standing instructions: the saved text, or the editor over it. The
// text is the parent's; this owns only the draft. The parent remounts it on a
// cluster switch so a draft never leaks onto another cluster.
import { useState } from 'react';

import { Button } from '@kubetail/ui/elements/button';

import { Textarea } from '@/components/widgets/text-entry';

export function MemoryInstructions({ text, onSave }: { text: string; onSave: (text: string) => void }) {
  const [draft, setDraft] = useState<string | null>(null);
  const editing = draft !== null;

  const save = () => {
    onSave(draft ?? '');
    setDraft(null);
  };

  let body;
  if (editing) {
    body = (
      <div className="flex flex-col gap-2">
        <Textarea
          prose
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          className="h-20 resize-y text-xs"
          autoFocus
        />
        <div className="flex items-center justify-between">
          {/* Clear empties the draft alone; Save commits it, even empty, unless
              there was nothing to begin with. */}
          <Button
            variant="link"
            size="xs"
            className="px-0 text-muted-foreground"
            disabled={draft === ''}
            onClick={() => setDraft('')}
          >
            Clear text
          </Button>
          <div className="flex gap-1.5">
            <Button variant="ghost" size="xs" className="h-7 px-3" onClick={() => setDraft(null)}>
              Cancel
            </Button>
            <Button size="xs" className="h-7 px-3" disabled={draft === '' && text === ''} onClick={save}>
              Save
            </Button>
          </div>
        </div>
      </div>
    );
  } else if (text !== '') {
    body = (
      <div className="flex items-start gap-3 rounded-md border-l-2 border-primary bg-background px-3 py-2">
        <p className="flex-1 text-xs text-card-foreground">{text}</p>
        <Button variant="link" size="xs" className="h-auto px-0" onClick={() => setDraft(text)}>
          Edit
        </Button>
      </div>
    );
  } else {
    body = (
      <div className="flex items-center justify-between py-2">
        <p className="text-xs text-muted-foreground">No instructions added</p>
        <Button variant="link" size="xs" className="h-auto px-0" onClick={() => setDraft('')}>
          Add instructions
        </Button>
      </div>
    );
  }

  return (
    // In line with the memory list's text, past its checkbox column, and as
    // far in on the right.
    <div className="flex flex-col gap-1.5 px-3">
      <p className="text-xs font-medium text-muted-foreground">INSTRUCTIONS</p>
      {body}
    </div>
  );
}
