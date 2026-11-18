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
  it("switches Bedrock's fields with its credential tabs", async () => {
    const user = userEvent.setup();
    render(<ProviderSetupForm sources={['aws-connector', 'access-key']} onCancel={vi.fn()} />);

    expect(screen.getByRole('tablist', { name: 'Credentials' })).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'AWS connector', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Connector' })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Region' })).toHaveTextContent('us-east-1');

    await user.click(screen.getByRole('tab', { name: 'Access key' }));

    expect(screen.queryByRole('combobox', { name: 'Connector' })).toBeNull();
    expect(screen.getByRole('combobox', { name: 'Region' })).toHaveTextContent('us-east-1');
  });

  // No secret is typed into the webview until a mutation hands it to the keychain.
  it("draws Bedrock's access key boxes disabled", async () => {
    const user = userEvent.setup();
    render(<ProviderSetupForm sources={['aws-connector', 'access-key']} onCancel={vi.fn()} />);

    await user.click(screen.getByRole('tab', { name: 'Access key' }));

    for (const label of ['Access key ID', 'Secret access key']) {
      const box = screen.getByLabelText(label, { selector: 'input' });
      expect(box).toBeDisabled();
      expectCodeText(box);
    }
    expect(screen.getByLabelText('Secret access key', { selector: 'input' })).toHaveAttribute('type', 'password');
    expect(screen.getByText('Kstack keeps the key in your system keychain.')).toBeInTheDocument();
  });

  it("draws Gemini's Google connector as a connector picker", () => {
    render(<ProviderSetupForm sources={['google-connector', 'api-key']} onCancel={vi.fn()} />);

    expect(screen.getByRole('tab', { name: 'Google connector', selected: true })).toBeInTheDocument();
    expect(screen.getByRole('combobox', { name: 'Connector' })).toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Region' })).toBeNull();
  });

  // No secret is typed into the webview until a mutation hands it to the keychain.
  it("draws Gemini's API key box disabled", async () => {
    const user = userEvent.setup();
    render(<ProviderSetupForm sources={['google-connector', 'api-key']} onCancel={vi.fn()} />);

    await user.click(screen.getByRole('tab', { name: 'API key' }));

    const key = screen.getByLabelText('API key', { selector: 'input' });
    expect(key).toBeDisabled();
    expect(key).toHaveAttribute('type', 'password');
    expectCodeText(key);
    expect(screen.getByText('Kstack keeps the key in your system keychain.')).toBeInTheDocument();
  });

  it('collapses on Cancel', async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    render(<ProviderSetupForm sources={['aws-connector']} onCancel={onCancel} />);

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledOnce();
  });

  it('holds Set up down, so a press sends nothing and stores nothing', async () => {
    const user = userEvent.setup();
    const setItem = vi.spyOn(Storage.prototype, 'setItem');
    render(<ProviderSetupForm sources={['api-key']} onCancel={vi.fn()} />);

    const submit = screen.getByRole('button', { name: 'Set up' });
    expect(submit).toBeDisabled();
    await user.click(submit);

    expect(invokeMock).not.toHaveBeenCalled();
    expect(setItem).not.toHaveBeenCalled();
    setItem.mockRestore();
  });
});
