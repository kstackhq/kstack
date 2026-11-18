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
import { describe, expect, it } from 'vitest';

import { SandboxEnvironment } from './sandbox-environment';

// Placeholder: every value here is static until the sidecar serves the run's environment.
describe('SandboxEnvironment', () => {
  it("lists the variables from the shell and Kstack's own, marked as coming soon", () => {
    render(<SandboxEnvironment />);
    const group = screen.getByRole('group', { name: 'Environment' });
    expect(screen.getByText('From your shell').closest('li')).toHaveTextContent('PATHLANGTZ');
    expect(screen.getByText('Set by Kstack').closest('li')).toHaveTextContent('TERM=dumb');
    expect(within(group).getByText('Coming soon')).toBeInTheDocument();
    expect(within(group).getByRole('button', { name: 'Import from shell' })).toBeDisabled();
  });

  it('lists the variables never passed', async () => {
    render(<SandboxEnvironment />);
    await userEvent.click(screen.getByRole('button', { name: 'Never passed' }));
    const list = await screen.findByRole('list', { name: 'Never passed' });
    expect(within(list).getByText('SSH_AUTH_SOCK')).toBeInTheDocument();
  });
});
