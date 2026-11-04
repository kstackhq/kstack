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

// The app window's side of its log windows. The host keeps the pair (`log_windows.rs`):
// this provider subscribes to it, folds its frames into one map by label, and sends
// the app window's commands, each naming the page the subscribe answered.
import { Channel, invoke } from '@tauri-apps/api/core';
import { useNavigate } from '@tanstack/react-router';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

import { useActiveCluster } from '@/lib/active-cluster';
import { reportHostError } from '@/lib/error-bus';
import { useLogsView } from '@/lib/logs-view';
import type { LogsView } from '@/lib/logs-view';
import { useRightSidebar } from '@/lib/right-sidebar';

/** One live log window of this app window. */
export type LogWindow = { label: string; clusterId: string; chatId: string; callId: string };

// The host's frames this page reads; it ignores the others.
type Frame =
  | { snapshot: { windows: LogWindow[] } }
  | { opened: { window: LogWindow } }
  | { closed: { label: string } }
  | { showChat: { chatId: string } };

type LogWindowsContextValue = {
  /** Whether a command can be sent: the subscribe has answered and the cluster is known. */
  ready: boolean;
  /** The window on a chat's call, if it has one. */
  windowOf: (chatId: string, callId: string) => LogWindow | undefined;
  /** Moves a view into a window of its own, or surfaces the one it has. */
  open: (view: LogsView) => void;
  /** Surfaces the window on a call. */
  focus: (callId: string) => void;
};

export const LogWindowsContext = createContext<LogWindowsContextValue | null>(null);

export function LogWindowsProvider({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  const focused = useLogsView();
  const sidebar = useRightSidebar();
  const { clusterID } = useActiveCluster();
  const [page, setPage] = useState<number | null>(null);
  const [windows, setWindows] = useState<ReadonlyMap<string, LogWindow>>(() => new Map());
  // The last cluster check: the cluster the windows belong to, the page it ran
  // under, and how many snapshots had landed.
  const checked = useRef<{ cluster?: string; page: number | null; snapshots: number }>({ page: null, snapshots: 0 });
  const snapshots = useRef(0);

  // What a frame reads when it lands, and an open when its turn comes and when the
  // host answers it, not when it was queued.
  const latest = useRef({ focused, sidebar, navigate });
  useEffect(() => {
    latest.current = { focused, sidebar, navigate };
  });

  // Opens and closes go one at a time: the host's open is async and its close is not,
  // so a close sent behind an open could run before the open reserves its window.
  const queue = useRef(Promise.resolve());
  const enqueue = useCallback((command: () => Promise<void>) => {
    queue.current = queue.current.then(command).catch(reportHostError);
  }, []);

  // One channel for the provider's life, kept across the dev-mode remount so both
  // subscribes hand the host the same one. The host mints pages from one rising
  // counter, so the largest answer is the current page whichever subscribe it
  // answered last.
  const channel = useRef<Channel<Frame> | null>(null);
  useEffect(() => {
    channel.current ??= new Channel<Frame>((frame) => {
      if ('snapshot' in frame) {
        snapshots.current += 1;
        setWindows(new Map(frame.snapshot.windows.map((w) => [w.label, w])));
      } else if ('opened' in frame) {
        const w = frame.opened.window;
        setWindows((last) => new Map(last).set(w.label, w));
      } else if ('closed' in frame) {
        const { label } = frame.closed;
        setWindows((last) => {
          if (!last.has(label)) return last;
          const next = new Map(last);
          next.delete(label);
          return next;
        });
      } else if ('showChat' in frame) {
        latest.current.navigate({ to: '/chat/$chatId', params: { chatId: frame.showChat.chatId } });
      }
    });
    invoke<{ page: number }>('log_window_subscribe', { channel: channel.current }).then(
      (answer) => setPage((last) => Math.max(last ?? 0, answer.page)),
      reportHostError,
    );
  }, []);

  // The windows belong to the app window's cluster, so a change to another closes
  // them all. The first cluster after the subscribe is held with no close; the
  // windows are checked against it instead, once per snapshot (a reload can
  // interrupt a change, leaving another cluster's window) and once per page (a
  // close sent on a page the host has ended was refused). An undefined cluster
  // (before the clusters bookmark, no context left) changes nothing. `windows` is a
  // dependency so a snapshot is checked as it lands.
  useEffect(() => {
    if (page === null || clusterID === undefined) return;
    const last = checked.current;
    const unchecked = last.page !== page || last.snapshots !== snapshots.current;
    const close =
      (last.cluster !== undefined && last.cluster !== clusterID) ||
      (unchecked && [...windows.values()].some((w) => w.clusterId !== clusterID));
    checked.current = { cluster: clusterID, page, snapshots: snapshots.current };
    if (close) enqueue(() => invoke('log_window_close_all', { page }));
  }, [page, clusterID, windows, enqueue]);

  const open = useCallback(
    (view: LogsView) => {
      if (page === null) return;
      // A collapsed sidebar keeps its view and its grep, so a card's open carries them.
      const was = latest.current.focused.view;
      const grep = was?.callId === view.callId ? (was.grep ?? null) : null;
      enqueue(async () => {
        // A card drawn before a cluster change opens nothing.
        if (view.clusterId !== checked.current.cluster) return;
        await invoke('log_window_open', {
          page,
          clusterId: view.clusterId,
          chatId: view.chatId,
          callId: view.callId,
          grep,
        });
        // The view moved, so the sidebar lets it go, unless the user focused another
        // call while the window built. The sidebar's open state is per mode.
        const now = latest.current;
        if (now.focused.view?.callId !== view.callId) return;
        now.focused.clear();
        if (now.sidebar.mode === 'chat' && now.sidebar.open) now.sidebar.close();
      });
    },
    [page, enqueue],
  );

  const focus = useCallback(
    (callId: string) => {
      if (page !== null) invoke('log_window_focus', { page, callId }).catch(reportHostError);
    },
    [page],
  );

  // Every card asks on every render, so the windows are keyed by call once per change.
  const ready = page !== null && clusterID !== undefined;
  const value = useMemo<LogWindowsContextValue>(() => {
    const byCall = new Map([...windows.values()].map((w) => [`${w.chatId}/${w.callId}`, w]));
    return { ready, windowOf: (chatId, callId) => byCall.get(`${chatId}/${callId}`), open, focus };
  }, [ready, windows, open, focus]);
  return <LogWindowsContext.Provider value={value}>{children}</LogWindowsContext.Provider>;
}

export function useLogWindows(): LogWindowsContextValue {
  const ctx = useContext(LogWindowsContext);
  if (!ctx) throw new Error('useLogWindows must be used within a LogWindowsProvider');
  return ctx;
}
