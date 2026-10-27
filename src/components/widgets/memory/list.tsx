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

// One scope's saved memories: each row edits in place from its menu, and
// checking any row swaps the heading for a bulk bar. Every delete confirms. A
// memory may have been written by the model from cluster data, so every field is
// drawn as text, never as markdown.
import { Fragment, useState } from 'react';

import { Checkbox as CheckboxPrimitive } from '@base-ui/react/checkbox';
import { Check, Minus, MoreHorizontal } from 'lucide-react';

import { Button } from '@kubetail/ui/elements/button';
import { Checkbox } from '@kubetail/ui/elements/checkbox';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { Input } from '@kubetail/ui/elements/input';
import { Separator } from '@kubetail/ui/elements/separator';
import { Textarea } from '@kubetail/ui/elements/textarea';
import { cn } from '@kubetail/ui/lib/utils';

import { Dialog } from '@/components/widgets/dialog';
import type { Memory } from '@/lib/memories';
import { BODY_RULE, bodyFits, slugify, typedName, uniqueName } from './writes';

function Provenance({ memory }: { memory: Memory }) {
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span className="font-medium break-all">{memory.name}</span>
      <span className="shrink-0">
        {memory.writtenBy === 'User' ? 'by you' : 'by Kstack'} · {new Date(memory.updatedAt).toLocaleDateString()}
      </span>
    </div>
  );
}

// Edits one memory's body, or writes a new memory when there is none. The draft
// is fixed when it opens, so a change arriving meanwhile never replaces what the
// user is saving. A refused save keeps the draft and says why. A new memory's
// name is optional: left empty, it comes from the body, clear of `taken`.
function MemoryEditor({
  memory,
  taken,
  onSave,
  onCancel,
}: {
  memory?: Memory;
  // The names the scope holds.
  taken: ReadonlySet<string>;
  onSave: (name: string, body: string) => Promise<string | null>;
  onCancel: () => void;
}) {
  const [name, setName] = useState('');
  const [draft, setDraft] = useState(memory?.body ?? '');
  const [saving, setSaving] = useState(false);
  const [refusal, setRefusal] = useState('');
  const body = draft.trim();
  const bodyBroken = !bodyFits(body);
  const ready = body !== '' && !bodyBroken;

  const nameToSend = () => {
    if (memory) return memory.name;
    return name.replace(/-$/, '') || uniqueName(slugify(body) || 'memory', taken);
  };

  const save = async () => {
    setSaving(true);
    setRefusal('');
    const refused = await onSave(nameToSend(), body);
    if (refused) {
      setRefusal(refused);
      setSaving(false);
    }
  };

  return (
    <fieldset disabled={saving} className="flex flex-col gap-1.5 pt-0.5 pb-1">
      <Textarea
        aria-label="Body"
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        className="h-14 resize-y text-xs"
        autoFocus
      />
      {bodyBroken && <p className="text-xs text-destructive">{BODY_RULE}</p>}
      {!memory && (
        // Uncontrolled, so the caret stays where folding puts it: React moves a
        // controlled input's caret to the end.
        <Input
          aria-label="Name"
          placeholder="name (optional)"
          onChange={(e) => {
            const input = e.target;
            const folded = typedName(input.value);
            if (folded !== input.value) {
              const caret = typedName(input.value.slice(0, input.selectionStart ?? undefined)).length;
              input.value = folded;
              input.setSelectionRange(caret, caret);
            }
            setName(folded);
          }}
          className="h-8 font-mono text-xs"
        />
      )}
      {memory && <Provenance memory={memory} />}
      {memory?.writtenBy === 'Model' && (
        <p className="text-xs text-muted-foreground">Saving makes this your memory: Kstack will follow it.</p>
      )}
      {refusal !== '' && <p className="text-xs text-destructive">{refusal}</p>}
      <div className="flex justify-end gap-1.5">
        <Button variant="ghost" size="xs" className="h-7 px-3" onClick={onCancel}>
          Cancel
        </Button>
        <Button size="xs" className="h-7 px-3" disabled={!ready} onClick={save}>
          Save
        </Button>
      </div>
    </fieldset>
  );
}

function Row({
  memory,
  selecting,
  selected,
  onToggle,
  onEdit,
  onDelete,
}: {
  memory: Memory;
  selecting: boolean;
  selected: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  // The open menu holds the row's hover look: the cursor leaves the row for
  // the menu, and a plain :hover would drop the trigger from under it.
  const [menuOpen, setMenuOpen] = useState(false);
  const revealed = selecting || menuOpen;
  // Both hide without giving up their space, so the text never moves: the
  // actions trigger by opacity alone, staying in the tab order so a keyboard
  // reaches every row, the checkbox by visibility. Focus inside the row shows
  // both.
  const checkbox = revealed ? '' : 'invisible group-hover/row:visible group-focus-within/row:visible';
  const trigger = revealed ? '' : 'opacity-0 group-hover/row:opacity-100 group-focus-within/row:opacity-100';

  return (
    <div
      className={cn(
        // A row spans the list's inset (`-mx-1`), so its fill runs 8px into the
        // panel's padding. Its left padding, the box and the gap put the text
        // under the heading's `pl-3`; `pr-3.5` and the trigger's own padding
        // end the icon as far from the right.
        'group/row -mx-1 flex items-start gap-1 rounded-xl py-3 pr-3.5 pl-1 hover:bg-secondary focus-within:bg-secondary',
        selected && 'bg-card hover:bg-card focus-within:bg-card',
        menuOpen && !selected && 'bg-secondary',
      )}
    >
      <Checkbox
        checked={selected}
        onCheckedChange={onToggle}
        className={cn('mt-1 size-3 rounded-[3px] [&_svg]:size-2.5!', checkbox)}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <p className="text-sm break-words whitespace-pre-wrap text-card-foreground">{memory.body}</p>
        <Provenance memory={memory} />
      </div>
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger
          render={<Button variant="ghost" size="icon-xs" className={cn('text-muted-foreground', trigger)} />}
          aria-label={`Actions for ${memory.name}`}
        >
          <MoreHorizontal aria-hidden />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" sideOffset={4}>
          <DropdownMenuItem onClick={onEdit}>Edit</DropdownMenuItem>
          <DropdownMenuItem variant="destructive" onClick={onDelete}>
            Delete
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  );
}

// The library's `Checkbox` draws a check mark in every state, so the select-all
// control, which must show a dash while partly selected, draws its own.
function SelectAllCheckbox({ selected, total, onToggle }: { selected: number; total: number; onToggle: () => void }) {
  const all = selected === total;
  return (
    <CheckboxPrimitive.Root
      checked={all}
      indeterminate={!all}
      onCheckedChange={onToggle}
      className="mt-0.5 flex size-3 shrink-0 items-center justify-center rounded-[3px] border border-input bg-background outline-none focus-visible:ring-3 focus-visible:ring-ring/50 data-checked:border-primary data-checked:bg-primary data-checked:text-primary-foreground data-indeterminate:border-primary data-indeterminate:bg-primary data-indeterminate:text-primary-foreground"
    >
      <CheckboxPrimitive.Indicator className="grid place-content-center [&>svg]:size-2.5">
        {all ? <Check /> : <Minus />}
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}

function BulkBar({
  selected,
  total,
  onToggleAll,
  onCancel,
  onDelete,
}: {
  selected: number;
  total: number;
  onToggleAll: () => void;
  onCancel: () => void;
  onDelete: () => void;
}) {
  return (
    // Spans the panel's padding as the rows do, so the select-all box sits over
    // theirs and the buttons end where their actions icons do.
    <div className="-mx-2 flex items-center justify-between pr-5 pl-1">
      <div className="flex items-start gap-1">
        <SelectAllCheckbox selected={selected} total={total} onToggle={onToggleAll} />
        <div className="flex flex-col text-xs leading-4">
          <span className="font-medium">{selected} selected</span>
          <span className="text-muted-foreground">{total} memories in total</span>
        </div>
      </div>
      <div className="flex items-center gap-1">
        <Button variant="ghost" size="xs" className="h-7" onClick={onCancel}>
          Cancel
        </Button>
        <Button variant="destructive" size="xs" className="h-7" onClick={onDelete}>
          Delete ({selected})
        </Button>
      </div>
    </div>
  );
}

const plural = (n: number) => (n === 1 ? 'memory' : 'memories');

export function MemoryList({
  scope,
  memories,
  adding,
  onSave,
  onAddDone,
  onDelete,
}: {
  // Where the memories live, as the delete confirm names it.
  scope: string;
  memories: Memory[];
  // Whether the new-memory editor is open.
  adding: boolean;
  // Resolves to why the sidecar refused, or null once saved; no memory adds one.
  onSave: (memory: Memory | undefined, name: string, body: string) => Promise<string | null>;
  onAddDone: () => void;
  // Resolves to the ids still there.
  onDelete: (ids: string[]) => Promise<string[]>;
}) {
  const [checkedIds, setCheckedIds] = useState<Set<string>>(new Set());
  // The watch can drop a checked row, so only rows still listed count.
  const selectedIds = new Set(memories.filter((m) => checkedIds.has(m.id)).map((m) => m.id));
  // One row edits at a time; starting another discards the first's draft.
  const [editingId, setEditingId] = useState<string | null>(null);
  // The ids a confirm is open for: one row's, or the whole selection.
  const [deleting, setDeleting] = useState<string[] | null>(null);
  const [deleteFailed, setDeleteFailed] = useState(false);
  const selecting = selectedIds.size > 0;
  const taken = new Set(memories.map((m) => m.name));

  const toggle = (id: string) =>
    setCheckedIds((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const toggleAll = () =>
    setCheckedIds(selectedIds.size === memories.length ? new Set() : new Set(memories.map((m) => m.id)));

  const closeConfirm = () => {
    setDeleting(null);
    setDeleteFailed(false);
  };

  // A delete that partly fails keeps the confirm open on what is left, so a
  // retry asks only for those.
  const confirmDelete = async () => {
    if (!deleting) return;
    setDeleteFailed(false);
    const left = await onDelete(deleting);
    setCheckedIds((prev) => new Set([...prev].filter((id) => !deleting.includes(id) || left.includes(id))));
    if (left.length > 0) {
      setDeleting(left);
      setDeleteFailed(true);
      return;
    }
    closeConfirm();
  };

  const count = deleting?.length ?? 0;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-0.5">
      {selecting ? (
        <BulkBar
          selected={selectedIds.size}
          total={memories.length}
          onToggleAll={toggleAll}
          onCancel={() => setCheckedIds(new Set())}
          onDelete={() => setDeleting([...selectedIds])}
        />
      ) : (
        // Over the rows' text, past their checkbox column.
        <p className="pl-3 text-xs font-medium text-muted-foreground">SAVED MEMORIES</p>
      )}
      {/* The list runs 8px into the panel's padding, and the rows' fills with
          it. The inset is the list's, not each row's, so an editor's focus
          ring is not clipped by the scroller. */}
      <div className={cn('-mx-2 flex min-h-0 flex-1 flex-col overflow-y-auto px-1', selecting && 'gap-0.5')}>
        {adding && (
          <MemoryEditor
            taken={taken}
            onSave={async (name, body) => {
              const refused = await onSave(undefined, name, body);
              if (!refused) onAddDone();
              return refused;
            }}
            onCancel={onAddDone}
          />
        )}
        {memories.length === 0 && !adding && (
          <p className="py-2 pl-4 text-xs text-muted-foreground">No saved memories yet.</p>
        )}
        {memories.map((memory, i) => (
          <Fragment key={memory.id}>
            {!selecting && i > 0 && <Separator className="mx-3 w-auto" />}
            {editingId === memory.id ? (
              <MemoryEditor
                memory={memory}
                taken={taken}
                onSave={async (name, body) => {
                  const refused = await onSave(memory, name, body);
                  if (!refused) setEditingId(null);
                  return refused;
                }}
                onCancel={() => setEditingId(null)}
              />
            ) : (
              <Row
                memory={memory}
                selecting={selecting}
                selected={selectedIds.has(memory.id)}
                onToggle={() => toggle(memory.id)}
                onEdit={() => setEditingId(memory.id)}
                onDelete={() => setDeleting([memory.id])}
              />
            )}
          </Fragment>
        ))}
      </div>
      <Dialog
        open={deleting !== null}
        onOpenChange={(open) => !open && closeConfirm()}
        title={`Delete ${count} ${plural(count)}?`}
        description={`The ${count} selected ${plural(count)} will be permanently deleted from ${scope}. This cannot be undone.`}
      >
        {deleteFailed && (
          <p className="text-sm text-destructive">
            {count} {plural(count)} could not be deleted.
          </p>
        )}
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={closeConfirm}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={confirmDelete}>
            Delete {count} {plural(count)}
          </Button>
        </div>
      </Dialog>
    </div>
  );
}
