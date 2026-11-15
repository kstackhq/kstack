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

import { SidebarProvider } from '@kubetail/ui/elements/sidebar';
import { createRoute } from '@tanstack/react-router';

import { ChatNav } from '@/components/widgets/chat-nav';
import { LeftSidebar } from '@/components/widgets/left-sidebar';
import { ModeNav } from '@/components/widgets/mode-nav';
import { Route as rootRoute } from '@/routes/__root';

export const Route = createRoute({
  getParentRoute: () => rootRoute,
  path: '/mockups/sidebar',
  component: SidebarPreview,
});

function SidebarPreview() {
  return (
    <SidebarProvider className="h-(--app-min-h) min-h-0 justify-center bg-muted/30 py-6">
      <div className="relative flex min-h-0">
        <LeftSidebar header={<ModeNav />} nav={<ChatNav mode="chat" />} />
      </div>
    </SidebarProvider>
  );
}
