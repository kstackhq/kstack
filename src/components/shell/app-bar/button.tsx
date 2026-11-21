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

// The look of the app bar's round buttons: no resting fill, so the chrome shows
// through and only hover paints.
import { Button } from '@kubetail/ui/elements/button';
import { cn } from '@kubetail/ui/lib/utils';
import type { ComponentProps } from 'react';

import { APP_BAR_HOVER_CLASS } from '@/lib/control-style';

export function AppBarButton({ className, ...props }: ComponentProps<typeof Button>) {
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      className={cn('size-7.5 rounded-full', APP_BAR_HOVER_CLASS, className)}
      {...props}
    />
  );
}

// The 1×18px rule between the bar's sections.
export function AppBarDivider() {
  return <div className="h-4.5 w-px shrink-0 bg-sidebar-border" aria-hidden />;
}
