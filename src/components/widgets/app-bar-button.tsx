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

// The look every control in the app bar wears, in one place so the buttons and
// the history pill cannot drift apart.
import type { ComponentProps } from 'react';
import { Button, buttonVariants } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';

// `ghost` is the library variant with no resting fill, so the bar's tint shows
// through and only hover paints. That drops the border with it, hence
// `border-sidebar-border` — the same border color the floating sidebar card
// wears, matching the design's pill outline: the button's base already carries
// `border` at transparent, so a control that is not a `Button` needs the width
// as well as the color.
const SHAPE = 'rounded-full border-sidebar-border';

// Every control in the bar hovers to the design's `custom/bg-input-80` — a wash
// over the bar's own tint, not the library's default `hover:bg-muted` (a fixed
// surface color that would clash with `bg-topbar`). Exported so a control that
// composes its own hover (the filled avatar, the caption buttons) can still land
// on the same shade.
//
// Needs the `dark:hover:` form too, not just `hover:`: the library's `ghost`
// variant carries its own `dark:hover:bg-muted/50`, a separate variant chain from
// plain `hover:`, so `cn`'s tailwind-merge dedupes only a matching chain — leaving
// the library's dark hover to win unless this names the same one.
export const APP_BAR_HOVER_CLASS = 'hover:bg-topbar-hover dark:hover:bg-topbar-hover';

const VARIANTS = { variant: 'ghost', size: 'icon' } as const;

/** For a control that cannot be a `Button` — the history pill's wrapper. */
export const APP_BAR_CONTROL_CLASS = SHAPE;

/**
 * For a control that must render as something else — `HomeButton` is a router
 * `Link`, which the `Button` element cannot be without taking `role="button"`
 * and ceasing to be a link.
 */
export function appBarButtonClass(className?: string) {
  return cn(buttonVariants(VARIANTS), SHAPE, APP_BAR_HOVER_CLASS, className);
}

export function AppBarButton({ className, ...props }: ComponentProps<typeof Button>) {
  return <Button {...VARIANTS} className={cn(SHAPE, APP_BAR_HOVER_CLASS, className)} {...props} />;
}
