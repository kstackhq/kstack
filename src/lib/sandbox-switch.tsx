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

// A chat's sandbox switch in flight. A send says which switch its sender saw and
// the sidecar refuses one that differs, so Send and Ask again hold off from the
// press until the list watch shows the value the mutation committed: the
// mutation's answer and the watch's frame are separate streams, and a send
// between them would pass the old value. The provider sits above the routes,
// like the outbox, so a pane that remounts meanwhile still waits.
import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import { useMutation } from 'urql';

import { graphql } from '@/gql';

const ChatSandboxDisabledSetMutation = graphql(`
  mutation ChatSandboxDisabledSet($id: ChatID!, $sandboxDisabled: Boolean!) {
    chatSandboxDisabledSet(id: $id, sandboxDisabled: $sandboxDisabled) {
      id
      sandboxDisabled
    }
  }
`);

// By chat: null while the mutation is in flight, then the value it committed
// until the watch shows it.
type InFlight = Record<string, boolean | null>;

type SandboxSwitchContextValue = {
  inFlight: InFlight;
  setInFlight: (next: (current: InFlight) => InFlight) => void;
};

const SandboxSwitchContext = createContext<SandboxSwitchContextValue | null>(null);

export function SandboxSwitchProvider({ children }: { children: ReactNode }) {
  const [inFlight, setInFlight] = useState<InFlight>({});
  const value = useMemo(() => ({ inFlight, setInFlight }), [inFlight]);
  return <SandboxSwitchContext.Provider value={value}>{children}</SandboxSwitchContext.Provider>;
}

function without(current: InFlight, chatID: string): InFlight {
  const next = { ...current };
  delete next[chatID];
  return next;
}

export type SandboxSwitching = {
  /** Whether a switch of the chat is in flight, or committed and not yet watched. */
  switching: boolean;
  setSandboxDisabled: (disabled: boolean) => void;
};

/**
 * The chat's switch, given the value the pane's list watch shows. errorReportExchange
 * reports a failure.
 */
export function useSandboxSwitch(chatID: string, watched: boolean | undefined): SandboxSwitching {
  const ctx = useContext(SandboxSwitchContext);
  if (!ctx) throw new Error('useSandboxSwitch must be used within a SandboxSwitchProvider');
  const { inFlight, setInFlight } = ctx;
  const [, set] = useMutation(ChatSandboxDisabledSetMutation);
  const committed = inFlight[chatID];
  const landed = committed !== undefined && committed !== null && committed === watched;

  useEffect(() => {
    if (landed) setInFlight((current) => without(current, chatID));
  }, [landed, chatID, setInFlight]);

  // A pane that mounts later opens its own list watch, whose snapshot follows a
  // commit that has answered, so the wait goes with the pane that started it. A
  // switch still in flight stays for the next pane.
  useEffect(
    () => () => setInFlight((current) => (current[chatID] === null ? current : without(current, chatID))),
    [chatID, setInFlight],
  );

  const setSandboxDisabled = async (disabled: boolean) => {
    setInFlight((current) => ({ ...current, [chatID]: null }));
    const result = await set({ id: chatID, sandboxDisabled: disabled });
    const value = result.data?.chatSandboxDisabledSet.sandboxDisabled;
    setInFlight((current) => (value === undefined ? without(current, chatID) : { ...current, [chatID]: value }));
  };

  return { switching: committed !== undefined && !landed, setSandboxDisabled };
}
