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

// Minimize/maximize/close for the frameless Linux/Windows window — no OS caption
// buttons there. `AppBar` mounts them only off macOS, which draws its own. Every
// control in the bar hovers to the same `custom/bg-input-80` wash, close included.
import { Minus, Square, X } from 'lucide-react';
import { getCurrentWindow } from '@tauri-apps/api/window';

import { APP_BAR_HOVER_CLASS } from '@/components/widgets/app-bar-button';

function minimize() {
  getCurrentWindow()
    .minimize()
    .catch(() => {});
}

function toggleMaximize() {
  getCurrentWindow()
    .toggleMaximize()
    .catch(() => {});
}

function close() {
  getCurrentWindow()
    .close()
    .catch(() => {});
}

// Shared button styling, hover included: every control in the bar wears the same
// `custom/bg-input-80` wash.
const BUTTON_CLASS = `flex h-full w-8 items-center justify-center outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${APP_BAR_HOVER_CLASS}`;

const CONTROLS = [
  { label: 'Minimize', onClick: minimize, icon: <Minus className="h-4 w-4" aria-hidden /> },
  { label: 'Maximize', onClick: toggleMaximize, icon: <Square className="h-3.5 w-3.5" aria-hidden /> },
  { label: 'Close', onClick: close, icon: <X className="h-4 w-4" aria-hidden /> },
] as const;

export function WindowControls() {
  return (
    <div className="flex items-center">
      {CONTROLS.map(({ label, onClick, icon }) => (
        <button key={label} type="button" aria-label={label} onClick={onClick} className={BUTTON_CLASS}>
          {icon}
        </button>
      ))}
    </div>
  );
}
