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

// The Settings dialog's Providers tab: every provider, in one alphabetical
// list, each set up or not, and each set up or configured from a dialog.
import { Button } from '@kubetail/ui/elements/button';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@kubetail/ui/elements/dropdown-menu';
import { cn } from '@kubetail/ui/lib/utils';
import { MoreHorizontal, RotateCw } from 'lucide-react';
import { useState } from 'react';

import { Dialog } from '@/components/primitives/dialog';
import { VisibleText } from '@/components/primitives/visible-text';
import { type Catalog, providersOf, useModels } from '@/lib/models';
import { useSelectSettingsTab } from '@/lib/settings-tab';

import { Intro } from './group';
import { type CredentialSource, ProviderSetupForm } from './provider-setup-form';

type Provider = {
  id: string;
  label: string;
  sources: CredentialSource[];
  /** The environment variable its key is read from. */
  keyVar?: string;
  /** What a provider with no key variable lacks until it is set up. */
  missing?: string;
  /** Whether the catalog serves it. */
  configured: boolean;
};

type Entry = Omit<Provider, 'configured'>;

const keyed = (id: string, label: string, keyVar: string, ...connectors: CredentialSource[]): Entry => ({
  id,
  label,
  keyVar,
  sources: ['environment', 'api-key', ...connectors],
});

// Placeholder: awaits a query listing every provider that can be configured.
// The keyed rows are the sidecar's catalog (catalog/providers.go), under its
// labels and key variables, so a row keeps its name once configured; the cloud
// platforms, Ollama and the ChatGPT connector await the LLM rebuild.
const PROVIDERS: Entry[] = [
  keyed('anthropic', 'Anthropic', 'ANTHROPIC_API_KEY'),
  keyed('openai', 'OpenAI', 'OPENAI_API_KEY', 'chatgpt-connector'),
  keyed('gemini', 'Gemini', 'GEMINI_API_KEY', 'google-connector'),
  keyed('groq', 'Groq', 'GROQ_API_KEY'),
  keyed('mistral', 'Mistral', 'MISTRAL_API_KEY'),
  keyed('deepseek', 'DeepSeek', 'DEEPSEEK_API_KEY'),
  keyed('xai', 'xAI', 'XAI_API_KEY'),
  keyed('openrouter', 'OpenRouter', 'OPENROUTER_API_KEY'),
  keyed('together', 'Together', 'TOGETHER_API_KEY'),
  keyed('fireworks', 'Fireworks', 'FIREWORKS_API_KEY'),
  keyed('opencode-go', 'OpenCode Go', 'OPENCODE_GO_API_KEY'),
  { id: 'bedrock', label: 'Amazon Bedrock', sources: ['aws-connector', 'access-key'], missing: 'Needs credentials' },
  { id: 'vertex', label: 'Google Vertex AI', sources: ['google-connector'], missing: 'Needs a Google connector' },
  { id: 'foundry', label: 'Microsoft Foundry', sources: ['azure-connector', 'api-key'], missing: 'Needs credentials' },
  { id: 'ollama', label: 'Ollama', sources: ['host'], missing: 'No host set' },
];

const CONNECTOR_SOURCES = new Set<CredentialSource>([
  'aws-connector',
  'google-connector',
  'azure-connector',
  'chatgpt-connector',
]);

// Every provider, by name: the ones the catalog serves, under its labels, then
// the rest of PROVIDERS. A served provider PROVIDERS does not list has no sources.
function providersFrom(models: Catalog['models']): Provider[] {
  const served = providersOf(models);
  const configured = served.map(({ id, label }) => ({
    sources: [],
    ...PROVIDERS.find((p) => p.id === id),
    id,
    label,
    configured: true,
  }));
  const rest = PROVIDERS.filter((p) => !served.some((s) => s.id === p.id)).map((p) => ({ ...p, configured: false }));
  return [...configured, ...rest].toSorted((a, b) => a.label.localeCompare(b.label));
}

export function ProvidersSettings() {
  return (
    <Intro lede="Configure your chats' models. Enable model providers with connectors or API keys.">
      <ProviderList catalog={useModels()} />
    </Intro>
  );
}

function ProviderList({ catalog: { models, loaded, failed, retry } }: { catalog: Catalog }) {
  // The provider whose dialog was last opened. It outlives `open` so the dialog
  // keeps its content while it animates closed.
  const [target, setTarget] = useState<Provider | null>(null);
  const [open, setOpen] = useState(false);
  if (failed) {
    return (
      <div>
        <Button type="button" variant="outline" size="sm" onClick={retry}>
          <RotateCw className="size-3.5" aria-hidden />
          Retry
        </Button>
      </div>
    );
  }
  // Nothing before the query answers: until then, every provider would read as not set up.
  if (!loaded) return null;
  return (
    <>
      <ul className="divide-y rounded-lg border">
        {providersFrom(models).map((p) => (
          <ProviderRow
            key={p.id}
            provider={p}
            onConfigure={() => {
              setTarget(p);
              setOpen(true);
            }}
          />
        ))}
      </ul>
      {target && (
        <Dialog
          open={open}
          onOpenChange={setOpen}
          title={
            <>
              {target.configured ? 'Configure' : 'Set up'} <VisibleText text={target.label} />
            </>
          }
        >
          <ProviderSetupForm
            key={target.id}
            sources={target.sources}
            // Placeholder: awaits a Provider field saying how its credentials
            // resolve. Until then a served provider's key variable counts as set.
            keyVar={target.keyVar ? { name: target.keyVar, set: target.configured } : undefined}
            onCancel={() => setOpen(false)}
          />
        </Dialog>
      )}
    </>
  );
}

// A provider's row: a status dot, its name over one detail line, its action on the right.
function ProviderRow({ provider, onConfigure }: { provider: Provider; onConfigure: () => void }) {
  const { label, configured, sources } = provider;
  return (
    <li className="flex items-center gap-3 px-4 py-2.5">
      {/* Filled when set up, a ring when not: shape as well as color, and the detail says it in words. */}
      <span
        aria-hidden
        data-slot="status"
        data-set-up={configured}
        className={cn(
          'size-2 shrink-0 rounded-full',
          configured ? 'bg-emerald-500' : 'border border-muted-foreground/50',
        )}
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span data-slot="name" className="truncate text-sm text-popover-foreground">
          <VisibleText text={label} />
        </span>
        <span className="truncate text-xs text-muted-foreground">
          <Detail provider={provider} />
        </span>
      </div>
      {configured ? (
        <RowMenu label={label} onConfigure={sources.length > 0 ? onConfigure : undefined} />
      ) : (
        <SetUpButton sources={sources} onSetUp={onConfigure} />
      )}
    </li>
  );
}

// Placeholder for a configured provider: awaits a Provider field saying how its
// credentials resolve. Until then a served provider's key variable counts as found.
function Detail({ provider: { configured, keyVar, missing } }: { provider: Provider }) {
  if (!configured) return missing ?? 'No API key found';
  if (!keyVar) return 'Configured';
  return (
    <>
      Found <code>{keyVar}</code>
    </>
  );
}

// Placeholder: awaits connectors. None is connected yet, so a provider only a
// connector sets up offers Connect, to the Connectors tab, in place of Set up.
function SetUpButton({ sources, onSetUp }: { sources: CredentialSource[]; onSetUp: () => void }) {
  const selectTab = useSelectSettingsTab();
  const needsConnector = sources.every((s) => CONNECTOR_SOURCES.has(s));
  return (
    <Button variant="outline" size="sm" onClick={needsConnector ? () => selectTab('Connectors') : onSetUp}>
      {needsConnector ? 'Connect' : 'Set up'}
    </Button>
  );
}

// Placeholder: Disable awaits its mutation, then a confirm Dialog. Configure is
// disabled for a provider with no source to pick.
function RowMenu({ label, onConfigure }: { label: string; onConfigure?: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={<Button variant="ghost" size="icon" className="size-7 rounded-md" />}
        aria-label={`${label} actions`}
      >
        <MoreHorizontal className="size-4" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" side="bottom">
        <DropdownMenuItem disabled={!onConfigure} onClick={onConfigure}>
          Configure…
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" disabled>
          Disable
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
