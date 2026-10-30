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
import type { ComponentProps, ReactNode } from 'react';
import { Button } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';

// `ghost` is the library variant with no resting fill, so the bar's tint shows
// through and only hover paints. That drops the border with it, hence
// `border-sidebar-border`: the button's base already carries `border` at
// transparent, so a control that is not a `Button` needs the width as well as
// the color.
const SHAPE = 'rounded-full border-sidebar-border';

// Every bar control is 32px tall: the omnibox, the groups and the buttons in them.
const HEIGHT = 'h-8';

// Hover is a wash over the bar's own tint rather than the library's fixed
// `bg-muted`. The `dark:` form is needed too: `ghost` carries its own
// `dark:hover:bg-muted/50`, which tailwind-merge only replaces with a class
// in the same variant chain.
export const APP_BAR_HOVER_CLASS = 'hover:bg-app-bar-hover dark:hover:bg-app-bar-hover';

const VARIANTS = { variant: 'ghost', size: 'icon' } as const;

/** For a control that cannot be a `Button` — the omnibox's wrapper. */
export const APP_BAR_CONTROL_CLASS = cn(SHAPE, HEIGHT);

export function AppBarButton({ className, ...props }: ComponentProps<typeof Button>) {
  return <Button {...VARIANTS} className={cn(SHAPE, 'size-8', APP_BAR_HOVER_CLASS, className)} {...props} />;
}

/**
 * Two or more controls drawn as one pill. The outline rides the group and the
 * buttons inside stay ghosts, with an `AppBarDivider` between them.
 */
export function AppBarGroup({ children }: { children: ReactNode }) {
  return <div className={cn('flex shrink-0 items-center border', HEIGHT, SHAPE)}>{children}</div>;
}

export function AppBarDivider() {
  return <div className="h-4.5 w-px shrink-0 bg-sidebar-border" aria-hidden />;
}
