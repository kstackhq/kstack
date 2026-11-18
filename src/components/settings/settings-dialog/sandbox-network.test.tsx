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
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { PermissionTarget } from '@/gql/graphql';

const { cloud } = vi.hoisted(() => ({ cloud: { current: undefined as PermissionTarget[] | undefined } }));
vi.mock('@/lib/sandbox-cloud-access', () => ({ useSandboxCloudAccess: () => cloud.current }));

const { SandboxNetwork } = await import('./sandbox-network');

const row = (label: string) => screen.getByText(label).closest('li')!;

beforeEach(() => {
  cloud.current = undefined;
});

describe('SandboxNetwork', () => {
  it('says how a command reaches your clusters', () => {
    render(<SandboxNetwork networkAvailable networkReason="" />);
    expect(screen.getByRole('group', { name: 'Network' })).toHaveTextContent('What a sandboxed command can reach.');
    expect(row('Your clusters')).toHaveTextContent('Through Kstack, under your Permissions settings.');
  });

  it('names the cloud platforms turned on, or says none is', () => {
    cloud.current = ['GitHub', 'AWS', 'GCP'];
    const { unmount } = render(<SandboxNetwork networkAvailable networkReason="" />);
    expect(row('Cloud platforms')).toHaveTextContent(
      'GitHub, AWS, and Google Cloud, through Kstack. Your sign-in stays outside.',
    );
    unmount();

    cloud.current = [];
    render(<SandboxNetwork networkAvailable networkReason="" />);
    expect(row('Cloud platforms')).toHaveTextContent('None. Every cloud platform is off.');
  });

  it("says the internet is off by default, or why it can't be given", () => {
    const { unmount } = render(<SandboxNetwork networkAvailable networkReason="" />);
    expect(row('Everything else')).toHaveTextContent(
      'Off by default. You can allow the internet for a chat or a command.',
    );
    unmount();

    render(<SandboxNetwork networkAvailable={false} networkReason="pasta not found " />);
    expect(screen.getByTestId('network-reason').textContent).toBe('pasta not found\\u{20}');
  });
});
