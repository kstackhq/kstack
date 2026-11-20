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

import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Connectors, OpenRouterConnection } from '@/lib/connectors';
import type { Catalog, Model } from '@/lib/models';

const { catalog, connectors } = vi.hoisted(() => ({
  catalog: { current: undefined as unknown as Catalog },
  connectors: { current: undefined as unknown as Connectors },
}));
vi.mock('@/lib/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/models')>()),
  useModels: () => catalog.current,
}));
vi.mock('@/lib/connectors', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/connectors')>()),
  useConnectors: () => connectors.current,
}));

const { SelectSettingsTab } = await import('@/lib/settings-tab');
const { ProvidersSettings } = await import('./providers');

function model(providerID: string, label: string, id: string): Model {
  return { provider: { id: providerID, label }, id, label: id, efforts: [], defaultEffort: '' };
}

function serve(models: Model[], state: Partial<Catalog> = {}) {
  catalog.current = { models, loaded: true, ...state };
}

const list = () => screen.getByRole('list');
const rows = () => within(list()).getAllByRole('listitem');
const names = () => rows().map((r) => r.querySelector('[data-slot="name"]')?.textContent);
const row = (name: string) => within(list()).getByText(name).closest('li')!;

// Every provider, by name.
const ALL = [
  'Amazon Bedrock',
  'Anthropic',
  'DeepSeek',
  'Fireworks',
  'Gemini',
  'Groq',
  'Microsoft Foundry',
  'Mistral',
  'Ollama',
  'OpenAI',
  'OpenCode Go',
  'OpenRouter',
  'Together',
  'Vertex AI',
  'xAI',
];

// What each provider no key variable sets up lacks.
const MISSING: Record<string, string> = {
  'Amazon Bedrock': 'Needs credentials',
  'Vertex AI': 'Needs a Google connector',
  'Microsoft Foundry': 'Needs credentials',
  Ollama: 'No host set',
};

// The connectors as the hook answers them: OpenRouter's alone matters here.
function openRouter(phase: OpenRouterConnection['phase'], loaded = true) {
  const actions = { connect: vi.fn(), cancel: vi.fn(), disconnect: vi.fn(), openKeys: vi.fn() };
  connectors.current = {
    loaded,
    connectors: [
      {
        id: 'openrouter',
        name: 'OpenRouter',
        detail: '',
        connected: phase === 'Connected',
        openrouter: { connection: { phase, failure: null }, actions },
      },
    ],
  };
}

beforeEach(() => {
  serve([]);
  openRouter('SignedOut');
});

describe('ProvidersSettings', () => {
  it('lists the catalog providers once each beside the ones that could be set up, by name', () => {
    serve([
      model('openai', 'OpenAI', 'gpt'),
      model('anthropic', 'Anthropic', 'opus'),
      model('openai', 'OpenAI', 'gpt-mini'),
      model('groq', 'Gro\u200Bq', 'oss'),
    ]);
    render(<ProvidersSettings />);

    expect(
      screen.getByText("Configure your chats' models. Enable model providers with connectors or API keys."),
    ).toBeInTheDocument();
    // The catalog's label wins over the list's.
    expect(names()).toEqual(ALL.map((n) => (n === 'Groq' ? 'Gro\\u{200B}q' : n)));
    expect(row('OpenAI')).toHaveTextContent('Found OPENAI_API_KEY');
  });

  it('gives a configured provider its key variable and a menu, whose Disable waits for its mutation', async () => {
    const user = userEvent.setup();
    serve([model('anthropic', 'Anthropic', 'opus')]);
    render(<ProvidersSettings />);

    expect(row('Anthropic')).toHaveTextContent('Found ANTHROPIC_API_KEY');
    expect(within(row('Anthropic')).queryByRole('button', { name: 'Set up' })).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Anthropic actions' }));

    expect(await screen.findByRole('menuitem', { name: 'Configure…' })).not.toHaveAttribute('aria-disabled');
    expect(screen.getByRole('menuitem', { name: 'Disable' })).toHaveAttribute('aria-disabled', 'true');
  });

  it("opens a configured provider's form on its environment from Configure…", async () => {
    const user = userEvent.setup();
    serve([model('openai', 'OpenAI', 'gpt')]);
    render(<ProvidersSettings />);

    await user.click(screen.getByRole('button', { name: 'OpenAI actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Configure…' }));

    const dialog = await screen.findByRole('dialog', { name: 'Configure OpenAI' });
    expect(within(dialog).getByRole('radio', { name: 'Environment variable' })).toBeChecked();
    expect(within(dialog).getByRole('radio', { name: 'ChatGPT connector' })).toBeInTheDocument();
    expect(dialog).toHaveTextContent('Kstack uses OPENAI_API_KEY from your environment.');
  });

  it('lists a provider the catalog serves beyond the known ones, with nothing to configure', async () => {
    const user = userEvent.setup();
    serve([model('fake', 'Fake', 'echo')]);
    render(<ProvidersSettings />);

    expect(row('Fake')).toHaveTextContent('Configured');
    await user.click(screen.getByRole('button', { name: 'Fake actions' }));
    expect(await screen.findByRole('menuitem', { name: 'Configure…' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('offers Set up on each provider that is not set up, and says what it lacks', () => {
    render(<ProvidersSettings />);

    expect(names()).toEqual(ALL);
    for (const name of ALL.filter((n) => n !== 'Vertex AI')) {
      expect(row(name)).toHaveTextContent(MISSING[name] ?? 'No API key found');
      expect(within(row(name)).getByRole('button', { name: 'Set up' })).toBeInTheDocument();
    }
  });

  it('sends a provider only a connector sets up to the Connectors tab', async () => {
    const user = userEvent.setup();
    const selectTab = vi.fn();
    render(
      <SelectSettingsTab value={selectTab}>
        <ProvidersSettings />
      </SelectSettingsTab>,
    );

    const vertex = row('Vertex AI');
    expect(vertex).toHaveTextContent('Needs a Google connector');
    expect(within(vertex).queryByRole('button', { name: 'Set up' })).toBeNull();
    await user.click(within(vertex).getByRole('button', { name: 'Connect' }));

    expect(selectTab).toHaveBeenCalledExactlyOnceWith('Connectors');
  });

  it('draws a provider the catalog serves once, as configured', () => {
    serve([model('gemini', 'Gemini', 'flash')]);
    render(<ProvidersSettings />);

    expect(names()).toEqual(ALL);
    expect(within(row('Gemini')).getByRole('button', { name: 'Gemini actions' })).toBeInTheDocument();
  });

  it("draws the Google connector's row once, configurable from its connector", async () => {
    const user = userEvent.setup();
    serve([model('google', 'Vertex AI', 'google/gemini-flash')]);
    render(<ProvidersSettings />);

    expect(names()).toEqual(ALL);
    expect(row('Vertex AI')).toHaveTextContent('Configured');
    await user.click(screen.getByRole('button', { name: 'Vertex AI actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Configure…' }));

    const dialog = await screen.findByRole('dialog', { name: 'Configure Vertex AI' });
    expect(within(dialog).getByRole('radio', { name: 'Google connector' })).toBeChecked();
  });

  it('marks each row set up or not with its status dot', () => {
    serve([model('openai', 'OpenAI', 'gpt')]);
    render(<ProvidersSettings />);

    const status = (name: string) => row(name).querySelector('[data-slot="status"]');
    expect(status('OpenAI')).toHaveAttribute('data-set-up', 'true');
    expect(status('Groq')).toHaveAttribute('data-set-up', 'false');
    expect(status('OpenAI')).toHaveAttribute('aria-hidden', 'true');
  });

  it('says a provider the OpenRouter connector serves is connected there, not found in the environment', async () => {
    const user = userEvent.setup();
    serve([model('openrouter', 'OpenRouter', 'auto')]);
    openRouter('Connected');
    render(<ProvidersSettings />);

    expect(row('OpenRouter')).toHaveTextContent('Connected on the Connectors tab');
    await user.click(screen.getByRole('button', { name: 'OpenRouter actions' }));
    await user.click(await screen.findByRole('menuitem', { name: 'Configure…' }));
    const dialog = await screen.findByRole('dialog', { name: 'Configure OpenRouter' });
    expect(dialog).toHaveTextContent('OPENROUTER_API_KEY is not set.');
  });

  it("offers the OpenRouter connector in OpenRouter's form", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);

    await user.click(within(row('OpenRouter')).getByRole('button', { name: 'Set up' }));

    const dialog = await screen.findByRole('dialog', { name: 'Set up OpenRouter' });
    expect(within(dialog).getByRole('radio', { name: 'OpenRouter connector' })).toBeInTheDocument();
  });

  it('draws nothing before the connectors answer', () => {
    openRouter('Connected', false);
    render(<ProvidersSettings />);

    expect(screen.queryByRole('list')).toBeNull();
  });

  it('draws nothing before the catalog answers', () => {
    serve([], { loaded: false });
    render(<ProvidersSettings />);

    expect(screen.queryByRole('list')).toBeNull();
  });

  it("opens a provider's form in a dialog from Set up, which Cancel closes", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);

    await user.click(within(row('Amazon Bedrock')).getByRole('button', { name: 'Set up' }));
    let dialog = await screen.findByRole('dialog', { name: 'Set up Amazon Bedrock' });
    expect(within(dialog).getByRole('radio', { name: 'AWS connector' })).toBeChecked();
    expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled();

    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());

    // The next provider's form starts afresh, on its own first source.
    await user.click(within(row('Gemini')).getByRole('button', { name: 'Set up' }));
    dialog = await screen.findByRole('dialog', { name: 'Set up Gemini' });
    expect(within(dialog).getByRole('radio', { name: 'Environment variable' })).toBeChecked();
    expect(dialog).toHaveTextContent('GEMINI_API_KEY is not set.');
  });
});
