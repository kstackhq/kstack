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
import { useState } from 'react';
import { beforeAll, describe, expect, it, vi } from 'vitest';

import type { Model, ModelPick } from '@/lib/models';
import { modelOf } from '@/lib/models';

import { ModelPicker } from './model-picker';

const fake: Model = {
  provider: { id: 'fake', label: 'Fake' },
  id: 'fake',
  label: 'Fake model',
  efforts: ['low', 'high'],
  defaultEffort: 'high',
};
const plain: Model = {
  provider: { id: 'fake', label: 'Fake' },
  id: 'plain',
  label: 'Plain model',
  efforts: [],
  defaultEffort: '',
};
const opus: Model = {
  provider: { id: 'anthropic', label: 'Anthropic' },
  id: 'claude-opus-5',
  label: 'Opus 5',
  efforts: ['low', 'high'],
  defaultEffort: 'high',
};

// The picker holds no pick of its own, so the harness keeps it as the outbox would.
function Harness({ models, effort = 'high' }: { models: Model[]; effort?: string }) {
  const [pick, setPick] = useState<ModelPick>({ model: { providerID: 'fake', id: 'fake' }, effort });
  return <ModelPicker models={models} pick={pick} picked={modelOf(models, pick.model)!} onPick={setPick} />;
}

const segment = (label: string) => screen.getByRole('button', { name: new RegExp(`^${label}:`) });

// The menu portals in asynchronously, so wait for the item rather than reading it.
const menuItem = (name: string) => screen.findByRole('menuitemradio', { name });

// base-ui scrolls the highlighted item into view; jsdom has no implementation.
beforeAll(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('ModelPicker', () => {
  it('picks the effort', async () => {
    const user = userEvent.setup();
    render(<Harness models={[fake, plain]} />);
    expect(segment('Model')).toHaveAccessibleName('Model: Fake model');
    expect(segment('Effort')).toHaveAccessibleName('Effort: high');

    await user.click(segment('Effort'));
    await user.click(await menuItem('low'));
    expect(segment('Effort')).toHaveAccessibleName('Effort: low');
  });

  // A stored pick can name a level the model has since dropped.
  it('names an effort the model does not list as it is stored', () => {
    render(<Harness models={[fake, plain]} effort="medium" />);
    expect(segment('Effort')).toHaveAccessibleName('Effort: medium');
    expect(segment('Effort')).toHaveTextContent('medium');
  });

  // The levels are the provider's own words, so the old pick may not exist on the
  // new model.
  it("resets the effort to the new model's default", async () => {
    const user = userEvent.setup();
    render(<Harness models={[fake, plain]} />);
    await user.click(segment('Effort'));
    await user.click(await menuItem('low'));

    await user.click(segment('Model'));
    await user.click(await menuItem('Plain model'));
    // Picking leaves the menu open, and while one is open the rest of the page takes
    // no pointer events.
    await user.keyboard('{Escape}');
    await user.click(screen.getByRole('button', { name: 'Model: Plain model' }));
    await user.click(await menuItem('Fake model'));

    expect(await screen.findByRole('button', { name: /^Effort:/ })).toHaveAccessibleName('Effort: high');
  });

  // A heading per provider, and only once there is more than one to tell apart.
  it('groups the models by provider when there is more than one', async () => {
    const user = userEvent.setup();
    render(<Harness models={[fake, plain, opus]} />);

    await user.click(segment('Model'));
    await menuItem('Opus 5');
    const menu = screen.getByRole('menu');
    expect(within(menu).getByText('Fake')).toBeInTheDocument();
    expect(within(menu).getByText('Anthropic')).toBeInTheDocument();
    // Catalog order: the fake's two, then Anthropic's one.
    expect(screen.getAllByRole('menuitemradio').map((item) => item.textContent)).toEqual([
      'Fake model',
      'Plain model',
      'Opus 5',
    ]);
  });

  it('draws no provider heading for a catalog of one provider', async () => {
    const user = userEvent.setup();
    render(<Harness models={[fake, plain]} />);

    await user.click(segment('Model'));
    await menuItem('Fake model');
    expect(within(screen.getByRole('menu')).queryByText('Fake')).toBeNull();
  });

  it('hides the effort select for a model with no such knob', async () => {
    const user = userEvent.setup();
    render(<Harness models={[fake, plain]} />);

    await user.click(segment('Model'));
    await user.click(await menuItem('Plain model'));

    expect(screen.queryByRole('button', { name: /^Effort:/ })).toBeNull();
  });
});
