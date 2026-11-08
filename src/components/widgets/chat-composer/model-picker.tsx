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

// The model and effort selects over the whole catalog, side by side as one control.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { ChevronDown } from 'lucide-react';
import type { ReactNode } from 'react';

import type { Model, ModelPick } from '@/lib/models';

type Option = { value: string; label: string };
type OptionGroup = { label?: string; options: Option[] };

// One of the picker's two selects: what is picked now, with the choices under it,
// in groups. A heading is drawn only when there is more than one group: one
// provider needs none. The two sit side by side as one control, so only the last
// one carries the chevron.
function Segment({
  label,
  value,
  groups,
  onSelect,
  leading,
  muted = false,
  chevron = false,
}: {
  label: string;
  value: string;
  groups: OptionGroup[];
  onSelect: (next: string) => void;
  /** Drawn before the picked label. */
  leading?: ReactNode;
  muted?: boolean;
  chevron?: boolean;
}) {
  const current = groups.flatMap((g) => g.options).find((o) => o.value === value);
  const headed = groups.length > 1;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="sm"
            className={`min-w-0 shrink gap-1.5 px-1.5 text-sm ${muted ? 'font-normal text-muted-foreground' : 'font-medium'}`}
          />
        }
        aria-label={`${label}: ${current?.label ?? value}`}
      >
        {leading}
        <span className="truncate">{current?.label ?? value}</span>
        {chevron && <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" sideOffset={6} className="max-h-80 min-w-40 overflow-y-auto">
        {/* Called with the value alone: base-ui also passes event details, which the
            setter would take as a stray second argument. */}
        <DropdownMenuRadioGroup value={value} onValueChange={(next: string) => onSelect(next)}>
          {groups.map((g, i) => (
            <DropdownMenuGroup key={g.label ?? i}>
              {headed && <DropdownMenuLabel>{g.label}</DropdownMenuLabel>}
              {g.options.map((o) => (
                <DropdownMenuRadioItem key={o.value} value={o.value}>
                  {o.label}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuGroup>
          ))}
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

// A model's key in the select: the id alone is not one, two providers being free to
// name a model the same.
const keyOf = (providerID: string, id: string) => `${providerID}/${id}`;

// The catalog as one group per provider, in catalog order, headed by its label.
function groupByProvider(models: Model[]): OptionGroup[] {
  const byProvider = new Map<string, OptionGroup>();
  models.forEach((m) => {
    const group = byProvider.get(m.provider.id) ?? { label: m.provider.label, options: [] };
    byProvider.set(m.provider.id, group);
    group.options.push({ value: keyOf(m.provider.id, m.id), label: m.label });
  });
  return [...byProvider.values()];
}

// The provider's mark beside the model: its initial, on its brand colour where
// there is one.
const PROVIDER_MARKS: Record<string, string> = {
  anthropic: 'bg-[#d97757] text-neutral-900',
};

function ProviderMark({ id, label }: { id: string; label: string }) {
  return (
    <span
      aria-hidden
      className={`flex size-4 shrink-0 items-center justify-center rounded-sm text-xs leading-none font-semibold ${PROVIDER_MARKS[id] ?? 'bg-muted text-muted-foreground'}`}
    >
      {label.charAt(0)}
    </span>
  );
}

type ModelPickerProps = {
  /** The catalog. */
  models: Model[];
  pick: ModelPick;
  /** The catalog's entry for `pick`. */
  picked: Model;
  onPick: (next: ModelPick) => void;
};

export function ModelPicker({ models, pick, picked, onPick }: ModelPickerProps) {
  const hasEffort = picked.efforts.length > 0;
  // Another model may not have the level this one is at, so the pick moves to the
  // new model's default rather than to a name it happens to share.
  const selectModel = (key: string) => {
    const next = models.find((m) => keyOf(m.provider.id, m.id) === key);
    /* v8 ignore next -- the menu offers only the catalog's keys */
    if (!next) return;
    onPick({ model: { providerID: next.provider.id, id: next.id }, effort: next.defaultEffort });
  };
  return (
    <div className="flex min-w-0 items-center">
      <Segment
        label="Model"
        value={keyOf(pick.model.providerID, pick.model.id)}
        groups={groupByProvider(models)}
        onSelect={selectModel}
        leading={<ProviderMark id={picked.provider.id} label={picked.provider.label} />}
        chevron={!hasEffort}
      />
      {/* A model with no such knob has nothing to show here. */}
      {hasEffort && (
        <Segment
          label="Effort"
          value={pick.effort}
          groups={[{ options: picked.efforts.map((e) => ({ value: e, label: e })) }]}
          onSelect={(effort) => onPick({ model: pick.model, effort })}
          muted
          chevron
        />
      )}
    </div>
  );
}
