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

// Notifications button in the app bar's right-hand group, beside Settings. There is
// no unread-notification source yet, so the badge is a placeholder dot rather than a
// fabricated count, and the popover holds placeholder copy until that surface is
// designed.
import { Bell } from 'lucide-react';

import { Popover, PopoverContent, PopoverTrigger } from '@kubetail/ui/elements/popover';

import { AppBarButton } from '@/components/widgets/app-bar-button';

export function NotificationButton() {
  return (
    <Popover>
      <PopoverTrigger render={<AppBarButton className="relative border-transparent" />} aria-label="Notifications">
        <Bell aria-hidden />
        <span aria-hidden className="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-primary ring-2 ring-topbar" />
      </PopoverTrigger>
      <PopoverContent align="end" sideOffset={8} className="w-72">
        <p className="text-sm font-medium">Notifications</p>
        <p className="mt-1 text-sm text-muted-foreground">Nothing here yet.</p>
      </PopoverContent>
    </Popover>
  );
}
