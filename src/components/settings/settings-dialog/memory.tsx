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

// The Settings dialog's Memory tab: the instructions and notes of one scope,
// every cluster or one.
import { Button } from '@kubetail/ui/elements/button';
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
} from '@kubetail/ui/elements/select';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { Plus } from 'lucide-react';
import { type ReactNode, useId, useState } from 'react';

import { MemoryInstructions } from '@/components/cluster/memory-instructions';
import { MemoryList } from '@/components/cluster/memory-list';
import { Dialog } from '@/components/primitives/dialog';
import { VisibleText } from '@/components/primitives/visible-text';
import { useActiveCluster } from '@/lib/active-cluster';
import { type Cluster, clusterForContext, displayName, useClusters } from '@/lib/clusters';
import { useKubeConfig } from '@/lib/kube-config';
import { type Memory, useMemories } from '@/lib/memories';
import { useMemoryWrites } from '@/lib/memory-writes';

// The picker's value for every cluster; a cluster's is its id.
const EVERY = '';

// Placeholder: awaits a count field on `Cluster`, or a `memoryCounts` query.
const noteCount = (_scope: string): number | undefined => undefined;

export function MemorySettings() {
  const { clusters } = useClusters();
  const { kubeConfig } = useKubeConfig();
  const { clusterID: activeID } = useActiveCluster();
  const [picked, setPicked] = useState(EVERY);
  const [clearing, setClearing] = useState(false);
  const labelID = useId();

  const present = (kubeConfig?.contexts ?? []).flatMap((ctx) => clusterForContext(clusters, ctx.name) ?? []);
  const archived = (clusters ?? [])
    .filter((c) => c.status.source.kubeconfig?.isPresent === false)
    .toSorted((a, b) => displayName(a).localeCompare(displayName(b)));
  // Undefined for every cluster, and once the picked record is gone.
  const cluster = [...present, ...archived].find((c) => c.id === picked);
  const name = cluster && displayName(cluster);
  const clusterID = cluster?.id ?? null;

  // Every cluster's notes ride on any cluster's watch.
  const watchID = cluster?.id ?? activeID ?? clusters?.[0]?.id;
  const { memories, phase } = useMemories(watchID);
  const notes = memories.filter((m) => m.clusterID === clusterID);

  let pending;
  if (!watchID && clusters !== null) {
    pending = <p className="text-xs text-muted-foreground">Kstack has not reached a cluster yet.</p>;
  } else if (!watchID || phase === 'connecting') {
    pending = <Spinner />;
  }

  return (
    <>
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col gap-1.5">
          <span id={labelID} className="text-xs text-muted-foreground">
            Applies to
          </span>
          <Select value={cluster?.id ?? EVERY} onValueChange={(next) => setPicked(next as string)}>
            <SelectTrigger size="sm" aria-labelledby={labelID} className="w-50">
              <SelectValue>
                <VisibleText text={name ?? 'Every cluster'} />
              </SelectValue>
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <ScopeItem value={EVERY} name="Every cluster" />
              <SelectSeparator />
              <ScopeGroup label="Kubeconfig" clusters={present} />
              {archived.length > 0 && <ScopeGroup label="Archived" clusters={archived} />}
            </SelectContent>
          </Select>
        </div>
        <Button variant="ghost" size="sm" className="text-destructive" onClick={() => setClearing(true)}>
          Clear memory
        </Button>
      </div>
      {/* Keyed on the scope, so no instructions or open editor carry over to another. */}
      <ScopeMemory
        key={clusterID}
        scope={name ?? 'every cluster'}
        clusterID={clusterID}
        notes={notes}
        pending={pending}
      />
      <Dialog
        open={clearing}
        onOpenChange={setClearing}
        title={<>Clear memory for {name ? <VisibleText text={name} /> : 'every cluster'}?</>}
        description={`This deletes its instructions and ${notes.length} notes.${cluster ? " Other clusters aren't affected." : ''}`}
      >
        <div className="flex justify-end gap-2">
          <Button variant="outline" onClick={() => setClearing(false)}>
            Cancel
          </Button>
          {/* Placeholder: awaits a `memoryClear(clusterID: ClusterID)` mutation. */}
          <Button variant="destructive" disabled>
            Clear
          </Button>
        </div>
      </Dialog>
    </>
  );
}

// One scope's instructions and notes. `pending` stands in for the list until
// the notes arrive.
function ScopeMemory({
  scope,
  clusterID,
  notes,
  pending,
}: {
  scope: string;
  clusterID: string | null;
  notes: Memory[];
  pending: ReactNode;
}) {
  const { save, remove } = useMemoryWrites();
  const [adding, setAdding] = useState(false);

  return (
    <>
      {/* Placeholder: local state, awaiting a stored instructions record per scope. */}
      <MemoryInstructions description="Given to every chat." />
      <section className="flex flex-col gap-2.5">
        <div className="flex items-end justify-between gap-3">
          <div className="flex flex-col gap-0.5">
            <h4 className="text-sm font-medium">Notes</h4>
            <p className="text-xs text-muted-foreground">
              Read at the start of each chat. You can edit or remove any note.
            </p>
          </div>
          <Button variant="outline" size="sm" disabled={adding || !!pending} onClick={() => setAdding(true)}>
            <Plus aria-hidden />
            Add note
          </Button>
        </div>
        {pending ?? (
          <MemoryList
            scope={scope}
            memories={notes}
            adding={adding}
            onSave={(memory, name, body) => save({ id: memory?.id ?? null, clusterID, name, body })}
            onAddDone={() => setAdding(false)}
            onDelete={remove}
          />
        )}
      </section>
    </>
  );
}

function ScopeGroup({ label, clusters }: { label: string; clusters: Cluster[] }) {
  return (
    <SelectGroup>
      <SelectLabel>{label}</SelectLabel>
      {clusters.map((c) => (
        <ScopeItem key={c.id} value={c.id} name={displayName(c)} />
      ))}
    </SelectGroup>
  );
}

function ScopeItem({ value, name }: { value: string; name: string }) {
  return (
    <SelectItem value={value}>
      <VisibleText text={name} />
      <span className="ml-auto text-xs text-muted-foreground">{noteCount(value)}</span>
    </SelectItem>
  );
}
