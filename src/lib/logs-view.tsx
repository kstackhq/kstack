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
// The focused view: the one log view a window draws in full, in chat mode's right
// sidebar. It is a LogsView call of a chat, the call's id and the action the sidecar
// resolved, held beside the right sidebar's state. It is chrome, not what the window
// is looking at, so it is out of the URL and not persisted: a new window opens with
// none. `set` also opens the right sidebar, since a view set with the panel closed is
// a view nobody sees. `viewer` is the store the viewer writes its state into, one
// per window, which the composer pulls at send.
import { createContext, useContext, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import type { ChatToolCall } from '@/lib/chats';
import type { ViewerStore } from '@/lib/log-viewer-store';
import { createViewerStore } from '@/lib/log-viewer-store';
import { useRightSidebar } from '@/lib/right-sidebar';

export type LogsViewAction = NonNullable<NonNullable<ChatToolCall['action']>['logsView']>;

export type LogsView = {
  chatId: string;
  callId: string;
  action: LogsViewAction;
};

type LogsViewContextValue = {
  view: LogsView | null;
  set: (view: LogsView) => void;
  clear: () => void;
  viewer: ViewerStore;
};

const LogsViewContext = createContext<LogsViewContextValue | null>(null);

export function LogsViewProvider({ children }: { children: ReactNode }) {
  const [view, setView] = useState<LogsView | null>(null);
  const [viewer] = useState(createViewerStore);
  const { show } = useRightSidebar();
  const value = useMemo<LogsViewContextValue>(
    () => ({
      view,
      set: (next) => {
        setView(next);
        show();
      },
      clear: () => setView(null),
      viewer,
    }),
    [view, show, viewer],
  );
  return <LogsViewContext.Provider value={value}>{children}</LogsViewContext.Provider>;
}

export function useLogsView(): LogsViewContextValue {
  const ctx = useContext(LogsViewContext);
  if (!ctx) throw new Error('useLogsView must be used within a LogsViewProvider');
  return ctx;
}
