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

// The folders sandboxed commands may read, and read and write: the always
// grants, a chat's own, and what no grant opens. The one reader of the
// sandboxFolders query and the folderGrant and folderRevoke mutations. After
// each change the query is asked again, since each folder's refused reason is
// the query's alone; another window can change the grants and each window
// caches its own, so it is also asked again whenever the window takes focus.
import { useCallback, useEffect, useState } from 'react';

import { useMutation, useQuery } from 'urql';
import type { CombinedError } from 'urql';

import { graphql } from '@/gql';
import type { GrantDuration, SandboxFoldersQuery } from '@/gql/graphql';

const SandboxFoldersDocument = graphql(`
  query SandboxFolders($chatID: ChatID) {
    sandboxFolders(chatID: $chatID) {
      always {
        id
        path
        write
        refused
      }
      chat {
        id
        path
        write
        refused
      }
      never
      wide
      rulesHeld
    }
  }
`);

const FolderGrantMutation = graphql(`
  mutation FolderGrant($chatID: ChatID, $path: String!, $write: Boolean!, $duration: GrantDuration!) {
    folderGrant(chatID: $chatID, path: $path, write: $write, duration: $duration) {
      wide
    }
  }
`);

const FolderRevokeMutation = graphql(`
  mutation FolderRevoke($id: String!) {
    folderRevoke(id: $id) {
      wide
    }
  }
`);

export type SandboxFolder = SandboxFoldersQuery['sandboxFolders']['always'][number];

/** A refused grant: the sidecar's reason, and the folder to grant instead when it names one (a link's target, or the disk's spelling). */
export type GrantRefusal = { message: string; target: string | null };

function refusalOf(error: CombinedError): GrantRefusal {
  const first = error.graphQLErrors[0];
  const target = first?.extensions?.target;
  return { message: first?.message ?? error.message, target: typeof target === 'string' ? target : null };
}

export type SandboxFolders = {
  /** Undefined until the sidecar has answered. */
  always: SandboxFolder[] | undefined;
  chat: SandboxFolder[] | undefined;
  never: string[] | undefined;
  /** The folders a read grant of which reads more than a project, resolved. */
  wide: string[] | undefined;
  /** The file holds a rule Kstack cannot read, so the sidecar refuses every Remove. */
  rulesHeld: boolean;
  granting: boolean;
  /** The ids whose Remove is in flight. */
  revoking: ReadonlySet<string>;
  /** The last refused grant, until the next one starts. */
  grantError: GrantRefusal | null;
  /** The last refused Remove's message, until the next one starts. */
  revokeError: string | null;
  /** Grants path; resolves whether the sidecar took it. */
  grant: (path: string, write: boolean, duration: GrantDuration) => Promise<boolean>;
  revoke: (id: string) => Promise<void>;
};

export function useSandboxFolders(chatID?: string): SandboxFolders {
  const chat = chatID ?? null;
  const [{ data }, reexecute] = useQuery({
    query: SandboxFoldersDocument,
    variables: { chatID: chat },
    requestPolicy: 'cache-and-network',
  });
  const [, grantMutation] = useMutation(FolderGrantMutation);
  const [, revokeMutation] = useMutation(FolderRevokeMutation);
  const [granting, setGranting] = useState(false);
  const [revoking, setRevoking] = useState<ReadonlySet<string>>(new Set());
  const [grantError, setGrantError] = useState<GrantRefusal | null>(null);
  const [revokeError, setRevokeError] = useState<string | null>(null);

  const askAgain = useCallback(() => reexecute({ requestPolicy: 'network-only' }), [reexecute]);
  useEffect(() => {
    window.addEventListener('focus', askAgain);
    return () => window.removeEventListener('focus', askAgain);
  }, [askAgain]);

  const grant = useCallback(
    async (path: string, write: boolean, duration: GrantDuration) => {
      setGranting(true);
      setGrantError(null);
      const { error } = await grantMutation({ chatID: chat, path, write, duration });
      setGranting(false);
      if (error) setGrantError(refusalOf(error));
      askAgain();
      return !error;
    },
    [grantMutation, chat, askAgain],
  );

  const revoke = useCallback(
    async (id: string) => {
      // Removes of different grants overlap, so each settles its own id.
      setRevoking((prev) => new Set(prev).add(id));
      setRevokeError(null);
      const { error } = await revokeMutation({ id });
      setRevoking((prev) => {
        const next = new Set(prev);
        next.delete(id);
        return next;
      });
      if (error) setRevokeError(error.graphQLErrors[0]?.message ?? error.message);
      askAgain();
    },
    [revokeMutation, askAgain],
  );

  const folders = data?.sandboxFolders;
  return {
    always: folders?.always,
    chat: folders?.chat,
    never: folders?.never,
    wide: folders?.wide,
    rulesHeld: folders?.rulesHeld ?? false,
    granting,
    revoking,
    grantError,
    revokeError,
    grant,
    revoke,
  };
}
