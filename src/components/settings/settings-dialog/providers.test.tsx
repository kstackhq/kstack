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

import type { Catalog, Model } from '@/lib/models';

const { catalog } = vi.hoisted(() => ({ catalog: { current: undefined as unknown as Catalog } }));
vi.mock('@/lib/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/models')>()),
  useModels: () => catalog.current,
}));

const { SelectSettingsTab } = await import('@/lib/settings-tab');
const { ProvidersSettings } = await import('./providers');

function model(providerID: string, label: string, id: string): Model {
  return { provider: { id: providerID, label }, id, label: id, efforts: [], defaultEffort: '' };
}

function serve(models: Model[], state: Partial<Catalog> = {}) {
  catalog.current = { models, loaded: true, failed: false, retry: vi.fn(), ...state };
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
  'Google Vertex AI',
  'Groq',
  'Microsoft Foundry',
  'Mistral',
  'Ollama',
  'OpenAI',
  'OpenCode Go',
  'OpenRouter',
  'Together',
  'xAI',
];

// What each provider no key variable sets up lacks.
const MISSING: Record<string, string> = {
  'Amazon Bedrock': 'Needs credentials',
  'Google Vertex AI': 'Needs a Google connector',
  'Microsoft Foundry': 'Needs credentials',
  Ollama: 'No host set',
};

beforeEach(() => serve([]));

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
    for (const name of ALL.filter((n) => n !== 'Google Vertex AI')) {
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

    const vertex = row('Google Vertex AI');
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

  it('marks each row set up or not with its status dot', () => {
    serve([model('openai', 'OpenAI', 'gpt')]);
    render(<ProvidersSettings />);

    const status = (name: string) => row(name).querySelector('[data-slot="status"]');
    expect(status('OpenAI')).toHaveAttribute('data-set-up', 'true');
    expect(status('Groq')).toHaveAttribute('data-set-up', 'false');
    expect(status('OpenAI')).toHaveAttribute('aria-hidden', 'true');
  });

  it('draws nothing before the catalog answers', () => {
    serve([], { loaded: false });
    render(<ProvidersSettings />);

    expect(screen.queryByRole('list')).toBeNull();
  });

  it('offers Retry alone when the catalog failed, and asks again on a press', async () => {
    const user = userEvent.setup();
    const retry = vi.fn();
    serve([], { loaded: false, failed: true, retry });
    render(<ProvidersSettings />);

    expect(screen.queryByRole('list')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
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
