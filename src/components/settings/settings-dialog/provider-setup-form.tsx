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

// The form a provider is set up from, drawn inside its row under Available.
// Placeholder throughout: nothing here sends or stores a credential.
import { Button } from '@kubetail/ui/elements/button';
import { Field, FieldDescription, FieldLabel } from '@kubetail/ui/elements/field';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@kubetail/ui/elements/select';
import { Tabs, TabsList, TabsTrigger } from '@kubetail/ui/elements/tabs';
import { useId, useState } from 'react';

import { Input } from '@/components/primitives/text-entry';

/** Where a provider's credentials can come from. */
export type CredentialSource = 'aws-connector' | 'access-key' | 'google-connector' | 'api-key';

const SOURCE_LABELS: Record<CredentialSource, string> = {
  'aws-connector': 'AWS connector',
  'access-key': 'Access key',
  'google-connector': 'Google connector',
  'api-key': 'API key',
};

// Placeholder: awaits connectors.
const CONNECTORS = ['Example connector'];

// Placeholder: awaits the Bedrock provider's region list.
const REGIONS = ['us-east-1', 'us-east-2', 'us-west-2', 'eu-central-1', 'eu-west-1', 'ap-northeast-1'];

export function ProviderSetupForm({ sources, onCancel }: { sources: CredentialSource[]; onCancel: () => void }) {
  const [source, setSource] = useState(sources[0]);
  return (
    <div className="flex flex-col gap-4">
      <Field>
        <FieldLabel>Credentials</FieldLabel>
        <Tabs value={source} onValueChange={(next) => setSource(next as CredentialSource)}>
          <TabsList aria-label="Credentials">
            {sources.map((s) => (
              <TabsTrigger key={s} value={s}>
                {SOURCE_LABELS[s]}
              </TabsTrigger>
            ))}
          </TabsList>
        </Tabs>
      </Field>
      <SourceFields source={source} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        {/* Placeholder: awaits a setup mutation per credential source. */}
        <Button type="button" size="sm" disabled>
          Set up
        </Button>
      </div>
    </div>
  );
}

function SourceFields({ source }: { source: CredentialSource }) {
  switch (source) {
    case 'aws-connector':
      return <AwsConnectorFields />;
    case 'api-key':
      return <ApiKeyField />;
    // Placeholder: awaits a design, then the mutation.
    case 'access-key':
    case 'google-connector':
      return <p className="text-sm text-muted-foreground">Coming soon</p>;
  }
}

// Placeholder: disabled, so no secret is typed into the webview, until a
// mutation hands the key to the sidecar for the keychain.
function ApiKeyField() {
  const id = useId();
  return (
    <Field>
      <FieldLabel htmlFor={id}>API key</FieldLabel>
      <Input id={id} type="password" disabled />
      <FieldDescription>Kstack keeps the key in your system keychain.</FieldDescription>
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
