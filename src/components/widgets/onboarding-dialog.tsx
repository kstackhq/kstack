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

// The onboarding flow: the sandbox's Settings sections walked in order —
// Programs, Executables, Permissions — each skippable with Next. Every change
// in a step is written at once by its own mutation, so closing halfway keeps
// what was done; only Finish writes the flag. A machine with no sandbox gets
// one screen in place of the three.
import { useEffect, useRef, useState } from 'react';

import { Button } from '@kubetail/ui/elements/button';

import { Dialog } from '@/components/widgets/dialog';
import { ContextModes, DefaultModePicker } from '@/components/widgets/permission-settings';
import { kubectlMissing, SandboxExecutableList, SandboxPathList } from '@/components/widgets/sandbox-settings';
import { type AppDialogProps } from '@/lib/dialog';
import { useOnboardingFinish } from '@/lib/onboarding';
import { isWindows } from '@/lib/platform';
import { usePermissionSettings } from '@/lib/permission-settings';
import { useSandbox } from '@/lib/sandbox';
import { useSandboxExecutables } from '@/lib/sandbox-executables';
import type { SandboxExecutables } from '@/lib/sandbox-executables';
import { useSandboxPath } from '@/lib/sandbox-path';

const STEPS = ['Programs', 'Executables', 'Permissions'] as const;
type Step = (typeof STEPS)[number];

function Stepper({ step }: { step: Step }) {
  return (
    <ol aria-label="Steps" className="flex gap-4">
      {STEPS.map((name, i) => (
        <li
          key={name}
          aria-current={name === step ? 'step' : undefined}
          className={name === step ? 'font-medium' : 'text-muted-foreground'}
        >
          {`${i + 1}. ${name}`}
        </li>
      ))}
    </ol>
  );
}

function ProgramsStep({ onRefreshed }: { onRefreshed: () => void }) {
  const path = useSandboxPath();
  const { entries } = path;
  const waiting = entries?.some((entry) => entry.state === 'Pending');
  return (
    <section aria-label="Programs" className="flex flex-col gap-2">
      <h3 className="font-medium">Programs</h3>
      <p className="text-muted-foreground">Sandboxed commands find programs in these folders.</p>
      {entries && !waiting && <p className="text-muted-foreground">Nothing is waiting for you.</p>}
      <SandboxPathList path={path} onRefreshed={onRefreshed} />
    </section>
  );
}

function ExecutablesStep({ executables }: { executables: SandboxExecutables }) {
  return (
    <section aria-label="Executables" className="flex flex-col gap-2">
      <h3 className="font-medium">Executables</h3>
      <p className="text-muted-foreground">
        The programs sandboxed commands run, and what each said when Kstack ran it in the sandbox.
      </p>
      {kubectlMissing(executables.report) && (
        <p role="alert" className="text-xs text-destructive">
          kubectl was not found on the sandbox&apos;s PATH. Install it, or go Back to Programs and include the folder it
          is in, then Probe again.
        </p>
      )}
      <SandboxExecutableList executables={executables} />
    </section>
  );
}

function PermissionsStep() {
  const permissions = usePermissionSettings();
  const { settings, error, held } = permissions;
  return (
    <section aria-label="Permissions" className="flex flex-col gap-4">
      <h3 className="font-medium">Permissions</h3>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {settings && (
        <>
          <DefaultModePicker permissions={permissions} />
          {held('modes') && (
            <p className="text-destructive">The file holds context modes Kstack cannot read. Fix them in Settings.</p>
          )}
          <ContextModes
            contexts={settings.contexts}
            disabled={held('modes')}
            onSet={permissions.setMode}
            onClear={permissions.clearMode}
          />
        </>
      )}
      <p className="text-muted-foreground">
        Set a production context to read-only here. Sandboxed commands have no network until you turn it on in a chat.
      </p>
    </section>
  );
}

// FinishButton writes the flag, then closes; a refusal stays on screen beside it.
function FinishButton({ label, onFinished }: { label: string; onFinished: () => void }) {
  const { finishing, finishError, finish } = useOnboardingFinish();
  return (
    <>
      {finishError && (
        <p role="alert" className="self-center text-destructive">
          {finishError}
        </p>
      )}
      <Button
        disabled={finishing}
        onClick={async () => {
          if (await finish()) onFinished();
        }}
      >
        {label}
      </Button>
    </>
  );
}

// useProbeOnFirstShow starts a probe the first time the Executables step is
// shown, when none runs and an executable is not probed yet. The launch probe
// has usually run by then, so it reads the report as it stands at that moment,
// waiting for the watch's first frame if none has come.
function useProbeOnFirstShow(shown: boolean, executables: SandboxExecutables) {
  const { report, probing, probe } = executables;
  const decided = useRef(false);
  useEffect(() => {
    if (!shown || decided.current || report === undefined) return;
    decided.current = true;
    if (!probing && report.some((executable) => !executable.probed)) probe();
  }, [shown, report, probing, probe]);
}

function SetupSteps({ onFinished }: { onFinished: () => void }) {
  const [index, setIndex] = useState(0);
  const step = STEPS[index];
  // One reader for the dialog's life, so a Refresh PATH on Programs starts the
  // probe the Executables step reads.
  const executables = useSandboxExecutables();
  useProbeOnFirstShow(step === 'Executables', executables);
  const last = index === STEPS.length - 1;

  return (
    <div className="flex flex-col gap-4 text-sm">
      <Stepper step={step} />
      {step === 'Programs' && <ProgramsStep onRefreshed={executables.probe} />}
      {step === 'Executables' && <ExecutablesStep executables={executables} />}
      {step === 'Permissions' && <PermissionsStep />}
      <div className="flex justify-end gap-2">
        <Button variant="outline" disabled={index === 0} onClick={() => setIndex(index - 1)}>
          Back
        </Button>
        {last ? (
          <FinishButton label="Finish" onFinished={onFinished} />
        ) : (
          <Button onClick={() => setIndex(index + 1)}>Next</Button>
        )}
      </div>
    </div>
  );
}

// The one screen on a machine with no sandbox. Native Windows has none by
// design, so only elsewhere is the sidecar's reason worth reading.
function NoSandbox({ reason, onFinished }: { reason: string; onFinished: () => void }) {
  return (
    <div className="flex flex-col gap-4 text-sm">
      <p>Kstack has no sandbox on this machine, so every command the model runs waits for you first.</p>
      {!isWindows() && reason && <p className="text-muted-foreground">{reason}</p>}
      <div className="flex justify-end">
        <FinishButton label="OK" onFinished={onFinished} />
      </div>
    </div>
  );
}

export function OnboardingDialog({ open, onOpenChange }: AppDialogProps) {
  const { available, reason } = useSandbox();
  const close = () => onOpenChange(false);
  return (
    <Dialog open={open} onOpenChange={onOpenChange} title="Set up the sandbox" className="sm:max-w-2xl">
      {available === true && <SetupSteps onFinished={close} />}
      {available === false && <NoSandbox reason={reason} onFinished={close} />}
    </Dialog>
  );
}
