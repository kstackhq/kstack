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

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { expectCodeText, mockTauriCore } from '@/test-utils';

const { invokeMock, factory } = mockTauriCore();
vi.mock('@tauri-apps/api/core', () => factory());

const { ProviderSetupForm } = await import('./provider-setup-form');

describe('ProviderSetupForm', () => {
  it("switches Bedrock's fields with its credential source", async () => {
    const user = userEvent.setup();
    render(<ProviderSetupForm sources={['aws-connector', 'access-key']} onCancel={vi.fn()} />);

    expect(screen.getByRole('radiogroup', { name: 'Credentials' })).toBeInTheDocument();
    expect(screen.getByRole('radio', { name: 'AWS connector' })).toBeChecked();
    expect(screen.getByRole('radio', { name: 'AWS connector' })).toHaveAccessibleDescription(
      'Sign in through an AWS connector.',
    );
    expect(screen.getByRole('combobox', { name: 'Connector' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Region' })).toHaveTextContent('us-east-1');

    await user.click(screen.getByRole('radio', { name: 'Access key' }));

    expect(screen.getByRole('radio', { name: 'Access key' })).toBeChecked();
    expect(screen.queryByRole('combobox')).toBeNull();
  });

  it('marks each source that awaits its design as coming soon', () => {
    render(
      <ProviderSetupForm
        sources={['environment', 'api-key', 'chatgpt-connector']}
        keyVar={{ name: 'OPENAI_API_KEY', set: true }}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getAllByText('Coming soon')).toHaveLength(1);
    expect(screen.getByRole('radio', { name: 'ChatGPT connector' })).toHaveAccessibleDescription(
      'Sign in with your ChatGPT account.',
    );
  });

  it.each([
    ['google-connector', 'Google connector', 'Sign in with your Google account.'],
    ['azure-connector', 'Azure connector', 'Sign in with your Azure account.'],
    ['host', 'Host', 'The address of the server that runs the models.'],
  ] as const)('describes %s, and draws no fields for it', (source, name, description) => {
    render(<ProviderSetupForm sources={[source]} onCancel={vi.fn()} />);

    expect(screen.getByRole('radio', { name })).toHaveAccessibleDescription(description);
    expect(screen.getByText('Coming soon')).toBeInTheDocument();
    expect(screen.queryByRole('textbox')).toBeNull();
  });

  // No secret is typed into the webview until a mutation hands it to the keychain.
  it('draws the saved key box disabled', async () => {
    const user = userEvent.setup();
    render(<ProviderSetupForm sources={['google-connector', 'api-key']} onCancel={vi.fn()} />);

    expect(screen.queryByLabelText('API key')).toBeNull();
    await user.click(screen.getByRole('radio', { name: 'Saved key' }));

    const key = screen.getByLabelText('API key', { selector: 'input' });
    expect(key).toBeDisabled();
    expect(key).toHaveAttribute('type', 'password');
    expectCodeText(key);
    expect(screen.getByRole('radio', { name: 'Saved key' })).toHaveAccessibleDescription(
      'A key Kstack keeps in your system keychain.',
    );
  });

  it('says whether the key variable is set, and that a saved key is used over it', () => {
    const { rerender } = render(
      <ProviderSetupForm
        sources={['environment', 'api-key']}
        keyVar={{ name: 'OPENAI_API_KEY', set: false }}
        onCancel={vi.fn()}
      />,
    );

    const env = () => screen.getByRole('radio', { name: 'Environment variable' });
    expect(env()).toBeChecked();
    expect(env()).toHaveAccessibleDescription(
      'OPENAI_API_KEY is not set. Kstack reads it from your login shell when it starts.',
    );
    expect(screen.queryByLabelText('API key')).toBeNull();

    rerender(
      <ProviderSetupForm
        sources={['environment', 'api-key']}
        keyVar={{ name: 'OPENAI_API_KEY', set: true }}
        onCancel={vi.fn()}
      />,
    );
    expect(env()).toHaveAccessibleDescription('Kstack uses OPENAI_API_KEY from your environment.');
    expect(screen.getByRole('radio', { name: 'Saved key' })).toHaveAccessibleDescription(
      'A key Kstack keeps in your system keychain, used over OPENAI_API_KEY.',
    );
  });

  it('collapses on Cancel', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<ProviderSetupForm sources={['aws-connector']} onCancel={onCancel} />);

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledOnce();
  });

  it('holds Save down, so a press sends nothing and stores nothing', async () => {
    const user = userEvent.setup();
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    render(<ProviderSetupForm sources={['api-key']} onCancel={vi.fn()} />);

    const submit = screen.getByRole('button', { name: 'Save' });
    expect(submit).toBeDisabled();
    await user.click(submit);

    expect(invokeMock).not.toHaveBeenCalled();
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();
  });
});
