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

// The frozen PATH sandboxed commands search, and the user's three changes to
// it: Include, Remove and Refresh PATH. The one reader of the queries and the
// mutations. After each change the query is asked again, since it alone
// carries the fault beside the list. Another window can change the list and
// each window caches its own, so it is also asked again on opening and
// whenever the window takes focus.
import { useCallback, useEffect, useState } from 'react';

import { useMutation, useQuery } from 'urql';
import type { CombinedError } from 'urql';

import { graphql } from '@/gql';
import type { SandboxPathQuery } from '@/gql/graphql';
import { refusalOf } from '@/lib/graphql/refusal';

const SandboxPathDocument = graphql(`
  query SandboxPath {
    sandboxPath {
      dir
      target
      state
      source
      shared
    }
    sandboxPathFault
    sandboxPathResolved
  }
`);

const SandboxPathIncludeMutation = graphql(`
  mutation SandboxPathInclude($dir: String!, $target: String!) {
    sandboxPathInclude(dir: $dir, target: $target) {
      dir
    }
  }
`);

const SandboxPathRemoveMutation = graphql(`
  mutation SandboxPathRemove($dir: String!) {
    sandboxPathRemove(dir: $dir) {
      dir
    }
  }
`);

const SandboxPathRefreshMutation = graphql(`
  mutation SandboxPathRefresh {
    sandboxPathRefresh {
      dir
    }
  }
`);

export type SandboxPathEntry = SandboxPathQuery['sandboxPath'][number];

export type SandboxPath = {
  /** Undefined until the sidecar has answered. */
  entries: SandboxPathEntry[] | undefined;
  /** Why the last read of the login shell failed, null once one answers. */
  fault: string | null | undefined;
  /** Whether a sync has read the shell: until one has, commands search the default PATH. */
  resolved: boolean | undefined;
  /** The dirs whose Include or Remove is in flight. */
  changing: ReadonlySet<string>;
  refreshing: boolean;
  /** The last refused Include's or Remove's message, until the next one starts. */
  changeError: string | null;
  /** The last refused refresh's message. */
  refreshError: string | null;
  /** Includes dir for target, the folder the user was shown. */
  include: (dir: string, target: string) => Promise<void>;
  remove: (dir: string) => Promise<void>;
  refresh: () => Promise<void>;
};

export function useSandboxPath(): SandboxPath {
  const [{ data }, reexecute] = useQuery({ query: SandboxPathDocument, requestPolicy: 'cache-and-network' });
  const [, includeMutation] = useMutation(SandboxPathIncludeMutation);
  const [, removeMutation] = useMutation(SandboxPathRemoveMutation);
  const [, refreshMutation] = useMutation(SandboxPathRefreshMutation);
  const [changing, setChanging] = useState<ReadonlySet<string>>(new Set());
  const [refreshing, setRefreshing] = useState(false);
  const [changeError, setChangeError] = useState<string | null>(null);
  const [refreshError, setRefreshError] = useState<string | null>(null);

  const askAgain = useCallback(() => reexecute({ requestPolicy: 'network-only' }), [reexecute]);
  useEffect(() => {
    window.addEventListener('focus', askAgain);
    return () => window.removeEventListener('focus', askAgain);
  }, [askAgain]);

  const change = useCallback(
    async (dir: string, run: () => Promise<{ error?: CombinedError }>) => {
      // Changes to different dirs overlap, so each settles only its own dir,
      // and one that succeeds leaves another's refusal on screen.
      setChanging((prev) => new Set(prev).add(dir));
      setChangeError(null);
      const { error } = await run();
      setChanging((prev) => {
        const next = new Set(prev);
        next.delete(dir);
        return next;
      });
      if (error) setChangeError(refusalOf(error));
      askAgain();
    },
    [askAgain],
  );
  const include = useCallback(
    (dir: string, target: string) => change(dir, () => includeMutation({ dir, target })),
    [change, includeMutation],
  );
  const remove = useCallback((dir: string) => change(dir, () => removeMutation({ dir })), [change, removeMutation]);

  const refresh = useCallback(async () => {
    setRefreshing(true);
    const { error } = await refreshMutation({});
    setRefreshing(false);
    setRefreshError(refusalOf(error));
    askAgain();
  }, [refreshMutation, askAgain]);

  return {
    entries: data?.sandboxPath,
    fault: data?.sandboxPathFault,
    resolved: data?.sandboxPathResolved,
    changing,
    refreshing,
    changeError,
    refreshError,
    include,
    remove,
    refresh,
  };
}
