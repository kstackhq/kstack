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

// The Sandbox console: a command run in a fresh sandboxed workspace, as a
// chat's would be, with the cluster and network the user picks. Its output is
// attacker-controlled text: control sequences stripped, drawn through VisibleText.
// Placeholder: awaits a mutation that runs a command and streams its output, so
// Run is disabled and only a test hands it history.
import { Button } from '@kubetail/ui/elements/button';
import { Checkbox } from '@kubetail/ui/elements/checkbox';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@kubetail/ui/elements/select';
import { stripAnsi } from 'fancy-ansi';
import { SquareTerminal, X } from 'lucide-react';
import { useState } from 'react';

import { DialogClose, DialogTitle, PanelDialog } from '@/components/primitives/dialog';
import { Input } from '@/components/primitives/text-entry';
import { VisibleText } from '@/components/primitives/visible-text';
import { humanDuration } from '@/lib/human-duration';
import { useKubeConfig } from '@/lib/kube-config';

export type ConsoleRun = { command: string; output: string; exitCode: number; ms: number };

const NO_RUNS: ConsoleRun[] = [];
const EXAMPLES = ['ls ~', 'cat ~/.ssh/config', 'curl -sI https://example.com', 'kubectl get ns'];

type SandboxConsoleProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  networkAvailable: boolean | undefined;
  history?: ConsoleRun[];
};

export function SandboxConsole({ open, onOpenChange, networkAvailable, history = NO_RUNS }: SandboxConsoleProps) {
  const { kubeConfig } = useKubeConfig();
  const [cluster, setCluster] = useState<string | null>(null);
  const [network, setNetwork] = useState(false);
  const [command, setCommand] = useState('');

  return (
    <PanelDialog open={open} onOpenChange={onOpenChange} className="h-140 w-200 flex-col bg-popover">
      <div className="flex items-center border-b px-4 py-2">
        <DialogTitle className="flex-1 text-center text-sm font-medium">Sandbox console</DialogTitle>
        <DialogClose aria-label="Close" render={<Button variant="ghost" size="icon" className="size-7" />}>
          <X className="size-4" />
        </DialogClose>
      </div>
      <div className="flex items-center gap-3 border-b px-4 py-2 text-sm">
        <span className="text-muted-foreground">Cluster</span>
        <Select value={cluster} onValueChange={setCluster}>
          <SelectTrigger size="sm" className="w-48" aria-label="Cluster">
            <SelectValue>
              {(v: string | null) => (v ? <VisibleText text={v} trailing="end" /> : 'No cluster')}
            </SelectValue>
          </SelectTrigger>
          <SelectContent alignItemWithTrigger={false}>
            <SelectItem value={null}>No cluster</SelectItem>
            {kubeConfig?.contexts.map(({ name }) => (
              <SelectItem key={name} value={name}>
                <VisibleText text={name} trailing="end" />
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {/* oxlint-disable-next-line jsx-a11y/label-has-associated-control -- Checkbox renders its control inside */}
        <label className="flex items-center gap-2">
          <Checkbox checked={network} disabled={!networkAvailable} onCheckedChange={setNetwork} />
          Network
        </label>
        <span className="ml-auto text-xs text-muted-foreground">Your always grants apply</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {history.length > 0 ? <Runs runs={history} /> : <Empty onPick={setCommand} />}
      </div>
      <div className="flex flex-col gap-2 border-t px-4 py-3">
        <div className="flex gap-2">
          <Input
            aria-label="Command"
            className="flex-1 font-mono"
            placeholder="Type a command"
            value={command}
            onChange={(e) => setCommand(e.target.value)}
          />
          <Button disabled>Run</Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Runs in a workspace of its own, as a chat&apos;s command would. Nothing here reaches a chat or the model.
        </p>
      </div>
    </PanelDialog>
  );
}

function Empty({ onPick }: { onPick: (command: string) => void }) {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-3 text-center">
      <div className="flex size-10 items-center justify-center rounded-md bg-muted">
        <SquareTerminal className="size-5" aria-hidden />
      </div>
      <p className="text-sm font-medium">Try a command in the sandbox</p>
      <p className="max-w-md text-xs text-muted-foreground">
        It runs the way a chat&apos;s command does: your always-granted folders, no network unless you tick Network, and
        only the cluster you pick.
      </p>
      <p className="text-xs text-muted-foreground">Try one</p>
      <div className="flex flex-wrap justify-center gap-2">
        {EXAMPLES.map((example) => (
          <Button key={example} variant="outline" size="sm" className="font-mono" onClick={() => onPick(example)}>
            {example}
          </Button>
        ))}
      </div>
    </div>
  );
}

function Runs({ runs }: { runs: ConsoleRun[] }) {
  return (
    <ul className="flex flex-col gap-4 font-mono text-xs">
      {runs.map((run, i) => (
        // oxlint-disable-next-line react/no-array-index-key -- the history only grows at its end
        <li key={i} className="flex flex-col gap-1">
          <p className="text-sm break-all whitespace-pre-wrap">
            <span className="text-muted-foreground">$ </span>
            <VisibleText text={run.command} trailing="end" />
          </p>
          <p className="break-all whitespace-pre-wrap" data-testid="output">
            <VisibleText text={stripAnsi(run.output)} trailing="end" />
          </p>
          <p className={run.exitCode !== 0 ? 'text-destructive' : 'text-muted-foreground'}>
            {`exit ${run.exitCode} · ${run.ms < 1000 ? `${run.ms}ms` : humanDuration(run.ms)}`}
          </p>
        </li>
      ))}
    </ul>
  );
}
