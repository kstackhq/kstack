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

// The look every control in the app bar wears, in one place so the buttons, the
// pills and the omnibox cannot drift apart.
import { Button } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';
import type { ComponentProps, ReactNode } from 'react';

import { APP_BAR_HEIGHT_CLASS, APP_BAR_HOVER_CLASS, APP_BAR_SHAPE_CLASS } from '@/lib/app-bar-style';

const VARIANTS = { variant: 'ghost', size: 'icon' } as const;

export function AppBarButton({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button {...VARIANTS} className={cn(APP_BAR_SHAPE_CLASS, 'size-8', APP_BAR_HOVER_CLASS, className)} {...props} />
  );
}

/**
 * Two or more controls drawn as one pill. The outline rides the group and the
 * buttons inside stay ghosts, with an `AppBarDivider` between them.
 */
export function AppBarGroup({ children }: { children: ReactNode }) {
  return (
    <div className={cn('flex shrink-0 items-center border', APP_BAR_HEIGHT_CLASS, APP_BAR_SHAPE_CLASS)}>{children}</div>
  );
}

export function AppBarDivider() {
  return <div className="h-4.5 w-px shrink-0 bg-sidebar-border" aria-hidden />;
}
