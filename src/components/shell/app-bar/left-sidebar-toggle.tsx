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

// The app bar's left-sidebar toggle: collapses and reopens the sidebar, or opens it as
// a drawer on a narrow window. Drives the same `@kubetail/ui` state as the built-in
// `Cmd/Ctrl+B`, so the two stay in sync.
import { Button } from '@kubetail/ui/elements/button';
import { useSidebar } from '@kubetail/ui/elements/sidebar';
import { cn } from '@kubetail/ui/lib/utils';
import { PanelLeft } from 'lucide-react';

import { APP_BAR_HOVER_CLASS } from '@/lib/control-style';

export function LeftSidebarToggle() {
  const { toggleSidebar } = useSidebar();
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label="Toggle sidebar"
      onClick={toggleSidebar}
      className={cn('size-7 shrink-0 rounded-md text-muted-foreground', APP_BAR_HOVER_CLASS)}
    >
      <PanelLeft className="size-4" aria-hidden />
    </Button>
  );
}
