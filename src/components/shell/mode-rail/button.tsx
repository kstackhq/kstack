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

// The look of the rail's buttons, links and buttons alike: a 30px square with no
// resting fill, washed on hover like the app bar's controls. The active mode
// (`data-status=active`, the router's mark) takes the page's fill and a shadow.
import { Button } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';
import type { ComponentProps } from 'react';

import { APP_BAR_HOVER_CLASS } from '@/lib/control-style';

export const RAIL_ITEM_CLASS = cn(
  'flex size-7.5 shrink-0 items-center justify-center rounded-[7.5px] text-muted-foreground transition-colors',
  '[&_svg]:size-3.75',
  APP_BAR_HOVER_CLASS,
  'data-[status=active]:bg-background data-[status=active]:text-foreground data-[status=active]:shadow-sm',
);

export function RailButton({ className, ...props }: ComponentProps<typeof Button>) {
  return <Button variant="ghost" size="icon" className={cn(RAIL_ITEM_CLASS, className)} {...props} />;
}
