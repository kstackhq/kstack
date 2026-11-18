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

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { Catalog, Model } from '@/lib/models';

const { catalog } = vi.hoisted(() => ({ catalog: { current: undefined as unknown as Catalog } }));
vi.mock('@/lib/models', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/lib/models')>()),
  useModels: () => catalog.current,
}));

const { ProvidersSettings } = await import('./providers');

function model(providerID: string, label: string, id: string): Model {
  return { provider: { id: providerID, label }, id, label: id, efforts: [], defaultEffort: '' };
}

function serve(models: Model[], state: Partial<Catalog> = {}) {
  catalog.current = { models, loaded: true, failed: false, retry: vi.fn(), ...state };
}

const setUp = () => screen.getByRole('group', { name: 'Set up' });
const available = () => screen.getByRole('group', { name: 'Available' });
const availableRows = () => within(available()).getAllByRole('listitem');

beforeEach(() => serve([]));

describe('ProvidersSettings', () => {
  it('lists each catalog provider once, in catalog order, under Set up', () => {
    serve([
      model('openai', 'OpenAI', 'gpt'),
      model('anthropic', 'Anthropic', 'opus'),
      model('openai', 'OpenAI', 'gpt-mini'),
      model('groq', 'Gro\u200Bq', 'oss'),
    ]);
    render(<ProvidersSettings />);

    expect(setUp()).toHaveTextContent(
      'The models chats can run on. Each takes its credentials from a connector, an API key or your environment.',
    );
    const rows = within(setUp()).getAllByRole('listitem');
    expect(rows.map((r) => r.querySelector('[data-slot="name"]')?.textContent)).toEqual([
      'OpenAI',
      'Anthropic',
      'Gro\\u{200B}q',
    ]);
    expect(rows[0]).toHaveTextContent('From your environment');
  });

  it('draws no rows and no empty state before the catalog answers', () => {
    serve([], { loaded: false });
    render(<ProvidersSettings />);

    expect(within(setUp()).queryByRole('listitem')).toBeNull();
    expect(screen.queryByText('No provider is set up.')).toBeNull();
  });

  it('says so when the catalog answers with no provider', () => {
    render(<ProvidersSettings />);

    expect(within(setUp()).getByText('No provider is set up.')).toBeInTheDocument();
  });

  it('offers Retry when the catalog failed, and asks again on a press', async () => {
    const user = userEvent.setup();
    const retry = vi.fn();
    serve([], { loaded: false, failed: true, retry });
    render(<ProvidersSettings />);

    expect(screen.queryByText('No provider is set up.')).toBeNull();
    await user.click(within(setUp()).getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it('gives each row a menu whose actions wait for their mutations', async () => {
    const user = userEvent.setup();
    serve([model('anthropic', 'Anthropic', 'opus')]);
    render(<ProvidersSettings />);

    await user.click(screen.getByRole('button', { name: 'Anthropic actions' }));

    expect(await screen.findByRole('menuitem', { name: 'Change credentials' })).toHaveAttribute(
      'aria-disabled',
      'true',
    );
    expect(screen.getByRole('menuitem', { name: 'Disable' })).toHaveAttribute('aria-disabled', 'true');
  });

  it('lists the providers that could be set up under Available, each with Set up', () => {
    render(<ProvidersSettings />);

    expect(available()).toHaveTextContent('Set up a provider with a connector or an API key.');
    const rows = within(available()).getAllByRole('listitem');
    expect(rows.map((r) => r.querySelector('[data-slot="name"]')?.textContent)).toEqual([
      'Amazon Bedrock',
      'Google Gemini',
    ]);
    expect(rows[0]).toHaveTextContent('Can use the AWS connector');
    expect(rows[1]).toHaveTextContent('Connect Google, or add an API key');
    rows.forEach((row) => expect(within(row).getByRole('button', { name: 'Set up' })).toBeInTheDocument());
  });

  it('leaves a provider the catalog serves out of Available', () => {
    serve([model('gemini', 'Gemini', 'flash')]);
    render(<ProvidersSettings />);

    expect(within(setUp()).getByText('Gemini')).toBeInTheDocument();
    expect(within(available()).queryByText('Google Gemini')).toBeNull();
    expect(within(available()).getByText('Amazon Bedrock')).toBeInTheDocument();
  });

  it("opens one row's form at a time from Set up, and Cancel collapses it", async () => {
    const user = userEvent.setup();
    render(<ProvidersSettings />);
    const row = (name: string) => within(available()).getByText(name).closest('li')!;

    await user.click(within(row('Amazon Bedrock')).getByRole('button', { name: 'Set up' }));
    expect(within(row('Amazon Bedrock')).getByRole('tab', { name: 'AWS connector' })).toBeInTheDocument();
    // The row's own Set up gives way to the form's, which is held down.
    expect(within(row('Amazon Bedrock')).getByRole('button', { name: 'Set up' })).toBeDisabled();

    await user.click(within(row('Google Gemini')).getByRole('button', { name: 'Set up' }));
    expect(within(row('Amazon Bedrock')).queryByRole('tablist')).toBeNull();
    expect(within(row('Google Gemini')).getByRole('tab', { name: 'Google connector' })).toBeInTheDocument();

    await user.click(within(row('Google Gemini')).getByRole('button', { name: 'Cancel' }));
    expect(screen.queryByRole('tablist')).toBeNull();
    availableRows().forEach((r) => expect(within(r).getByRole('button', { name: 'Set up' })).toBeEnabled());
  });
});
