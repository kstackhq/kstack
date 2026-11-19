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

// The form a provider is configured from, drawn in its dialog: a choice of
// where its credentials come from, then that source's fields. Nothing here
// sends or stores a credential: a real connector source sends the user to the
// Connectors tab, and the rest are placeholders.
import { Radio } from '@base-ui/react/radio';
import { RadioGroup } from '@base-ui/react/radio-group';
import { Button } from '@kubetail/ui/elements/button';
import { Field, FieldLabel } from '@kubetail/ui/elements/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@kubetail/ui/elements/select';
import { useId, useState } from 'react';

import { Input } from '@/components/primitives/text-entry';
import { useSelectSettingsTab } from '@/lib/settings-tab';

/** Where a provider's credentials can come from. */
export type CredentialSource =
  | 'environment'
  | 'aws-connector'
  | 'access-key'
  | 'google-connector'
  | 'azure-connector'
  | 'chatgpt-connector'
  | 'openrouter-connector'
  | 'api-key'
  | 'host';

// Each source's radio: its label and the line under it. Placeholder: a source
// marked `soon` awaits a design, then its mutation.
const SOURCES: Record<CredentialSource, { label: string; note?: string; soon?: true }> = {
  environment: { label: 'Environment variable' },
  'aws-connector': { label: 'AWS connector', note: 'Sign in through an AWS connector.' },
  'access-key': { label: 'Access key', note: 'An AWS access key ID and secret.', soon: true },
  'google-connector': { label: 'Google connector', note: 'Sign in with your Google account.' },
  'azure-connector': { label: 'Azure connector', note: 'Sign in with your Azure account.', soon: true },
  'chatgpt-connector': { label: 'ChatGPT connector', note: 'Sign in with your ChatGPT account.', soon: true },
  'openrouter-connector': { label: 'OpenRouter connector', note: 'Sign in with your OpenRouter account.' },
  'api-key': { label: 'Saved key', note: 'A key Kstack keeps in your system keychain.' },
  host: { label: 'Host', note: 'The address of the server that runs the models.', soon: true },
};

// The sources the Connectors tab sets up: the form sends the user there.
const ON_CONNECTORS_TAB = new Set<CredentialSource>(['google-connector', 'openrouter-connector']);

// Placeholder: awaits connectors.
const CONNECTORS = ['Example connector'];

// Placeholder: awaits the Bedrock provider's region list.
const REGIONS = ['us-east-1', 'us-east-2', 'us-west-2', 'eu-central-1', 'eu-west-1', 'ap-northeast-1'];

/** The environment variable a provider reads its key from, and whether Kstack found it. */
export type KeyVar = { name: string; set: boolean };

/** Opens on the first of `sources`. */
export function ProviderSetupForm({
  sources,
  keyVar,
  onCancel,
}: {
  sources: CredentialSource[];
  keyVar?: KeyVar;
  onCancel: () => void;
}) {
  const [source, setSource] = useState(sources[0]);
  const selectTab = useSelectSettingsTab();
  return (
    <div className="flex flex-col gap-4">
      <RadioGroup
        aria-label="Credentials"
        className="flex flex-col gap-3"
        value={source}
        onValueChange={(next: CredentialSource) => setSource(next)}
      >
        {sources.map((s) => (
          <SourceOption key={s} source={s} keyVar={keyVar} />
        ))}
      </RadioGroup>
      {source === 'aws-connector' && <AwsConnectorFields />}
      {source === 'api-key' && <ApiKeyField />}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        {ON_CONNECTORS_TAB.has(source) ? (
          <Button
            type="button"
            size="sm"
            onClick={() => {
              onCancel();
              selectTab('Connectors');
            }}
          >
            Open Connectors
          </Button>
        ) : (
          // Placeholder: awaits a mutation choosing the provider's credential source.
          <Button type="button" size="sm" disabled>
            Save
          </Button>
        )}
      </div>
    </div>
  );
}

// One source's radio: its label, with a line saying what it means.
function SourceOption({ source, keyVar }: { source: CredentialSource; keyVar?: KeyVar }) {
  const labelID = useId();
  const noteID = useId();
  const { label, soon } = SOURCES[source];
  return (
    <div className="flex items-start gap-2.5">
      <Radio.Root
        value={source}
        aria-labelledby={labelID}
        aria-describedby={noteID}
        className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded-full border border-input outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50 data-checked:border-primary"
      >
        <Radio.Indicator className="size-2 rounded-full bg-primary" />
      </Radio.Root>
      <div className="flex min-w-0 flex-col gap-0.5">
        <span className="flex items-center gap-2 text-sm text-popover-foreground">
          <span id={labelID}>{label}</span>
          {soon && <span className="text-xs text-muted-foreground">Coming soon</span>}
        </span>
        <span id={noteID} className="text-xs text-muted-foreground">
          <SourceNote source={source} keyVar={keyVar} />
        </span>
      </div>
    </div>
  );
}

// A source's note, naming the provider's key variable where it bears on the
// source. The sidecar reads that variable from its launch environment or the
// login shell, once, at start.
function SourceNote({ source, keyVar }: { source: CredentialSource; keyVar?: KeyVar }) {
  if (source === 'environment' && keyVar) {
    return keyVar.set ? (
      <>
        Kstack uses <code>{keyVar.name}</code> from your environment.
      </>
    ) : (
      <>
        <code>{keyVar.name}</code> is not set. Kstack reads it from your login shell when it starts.
      </>
    );
  }
  if (source === 'api-key' && keyVar?.set) {
    return (
      <>
        A key Kstack keeps in your system keychain, used over <code>{keyVar.name}</code>.
      </>
    );
  }
  return SOURCES[source].note;
}

// Placeholder: disabled, so no secret is typed into the webview, until a
// mutation hands the key to the sidecar for the keychain.
function ApiKeyField() {
  const id = useId();
  return (
    <Field>
      <FieldLabel htmlFor={id}>API key</FieldLabel>
      <Input id={id} type="password" disabled />
    </Field>
  );
}

function AwsConnectorFields() {
  return (
    <div className="flex gap-3">
      <PickField label="Connector" options={CONNECTORS} />
      <PickField label="Region" options={REGIONS} initial={REGIONS[0]} />
    </div>
  );
}

function PickField({ label, options, initial }: { label: string; options: string[]; initial?: string }) {
  const id = useId();
  return (
    <Field className="flex-1">
      <FieldLabel htmlFor={id}>{label}</FieldLabel>
      <Select defaultValue={initial}>
        <SelectTrigger id={id} className="w-full">
          <SelectValue placeholder={`Pick a ${label.toLowerCase()}`} />
        </SelectTrigger>
        <SelectContent alignItemWithTrigger={false}>
          {options.map((o) => (
            <SelectItem key={o} value={o}>
              {o}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}
