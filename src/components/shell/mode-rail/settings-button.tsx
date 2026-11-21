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

// Settings, at the mode rail's foot. The dialog it asks for renders in
// `AppDialogs`, above the sidebar: only the request is made here.
import { Settings } from 'lucide-react';

import { useDialog } from '@/lib/dialog';

import { RailButton } from './button';

export function SettingsButton() {
  const { openDialog } = useDialog();

  return (
    <RailButton aria-label="Settings" title="Settings" onClick={() => openDialog('settings')}>
      <Settings aria-hidden />
    </RailButton>
  );
}
