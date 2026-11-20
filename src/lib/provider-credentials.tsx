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

// Where each keyed provider's credential comes from, and the two mutations that
// save and remove a key. A key passes through here once, in a mutation's
// variables, and is never kept: no operation result is held in React state.
import { useCallback, useMemo } from 'react';
import { type CombinedError, useClient } from 'urql';

import { graphql } from '@/gql';
import type { ProviderCredentialSource, ProviderCredentialsWatchSubscription } from '@/gql/graphql';
import { perFrame, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';

export type { ProviderCredentialSource };

export type ProviderCredentialState = ProviderCredentialsWatchSubscription['providerCredentialsWatch'][number];

/** A save's outcome: the sidecar's refusal by its code, or any other failure. */
export type SaveKeyResult = 'saved' | 'bad-key' | 'not-saved' | 'failed';

export type ProviderCredentials = {
  credentials: ProviderCredentialState[];
  // The watch's first frame has landed; nothing draws before it.
  loaded: boolean;
  saveKey: (providerID: string, key: string) => Promise<SaveKeyResult>;
  // True once the saved key is gone.
  deleteKey: (providerID: string) => Promise<boolean>;
};

const ProviderCredentialsWatchDocument = graphql(`
  subscription ProviderCredentialsWatch {
    providerCredentialsWatch {
      id
      label
      keyVariable
      source
      hasEnvironmentKey
      hasSavedKey
      failure
    }
  }
`);

const ProviderKeySaveDocument = graphql(`
  mutation ProviderKeySave($providerID: String!, $key: String!) {
    providerKeySave(providerID: $providerID, key: $key)
  }
`);

const ProviderKeyDeleteDocument = graphql(`
  mutation ProviderKeyDelete($providerID: String!) {
    providerKeyDelete(providerID: $providerID)
  }
`);

const NO_CREDENTIALS: ProviderCredentialState[] = [];

export function useProviderCredentials(): ProviderCredentials {
  const { data } = useWatchSubscription<ProviderCredentialsWatchSubscription, ProviderCredentialState[]>(
    { query: ProviderCredentialsWatchDocument },
    perFrame((_prev, next) => next.providerCredentialsWatch),
    { unshared: true },
  );
  const client = useClient();
  // Not useMutation: its state would hold the operation, and with it the key.
  const saveKey = useCallback(
    async (providerID: string, key: string): Promise<SaveKeyResult> => {
      const { error } = await client.mutation(ProviderKeySaveDocument, { providerID, key }).toPromise();
      if (!error) return 'saved';
      switch (codeOf(error)) {
        case 'KSTACK_VALIDATION_ERROR':
          return 'bad-key';
        case 'KSTACK_PROVIDER_KEY_NOT_SAVED':
          return 'not-saved';
        default:
          return 'failed';
      }
    },
    [client],
  );
  const deleteKey = useCallback(
    async (providerID: string) => {
      const { error } = await client.mutation(ProviderKeyDeleteDocument, { providerID }).toPromise();
      return !error;
    },
    [client],
  );
  return useMemo(
    () => ({ credentials: data ?? NO_CREDENTIALS, loaded: data !== undefined, saveKey, deleteKey }),
    [data, saveKey, deleteKey],
  );
}

function codeOf(error: CombinedError): unknown {
  return error.graphQLErrors[0]?.extensions?.code;
}
