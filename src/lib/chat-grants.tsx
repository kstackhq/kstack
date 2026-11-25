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

// A chat's own Kubernetes rules: the ones an approval answered "Allow for this
// chat" wrote, which the composer lists and removes. The one reader of the
// query and the mutation. Another window can add or remove one, so the query
// is asked again on focus and after a removal; an approval in this window
// names the rule's type (`chatGrantsContext`), which asks it again too.
import { useCallback, useEffect, useState } from 'react';
import { useMutation, useQuery } from 'urql';

import { graphql } from '@/gql';
import type { ChatKubeRulesQuery } from '@/gql/graphql';

const ChatKubeRulesDocument = graphql(`
  query ChatKubeRules($chatID: ChatID!) {
    chatKubeRules(chatID: $chatID) {
      id
      line
    }
  }
`);

const ChatKubeRuleRemoveMutation = graphql(`
  mutation ChatKubeRuleRemove($chatID: ChatID!, $id: String!) {
    chatKubeRuleRemove(chatID: $chatID, id: $id) {
      id
    }
  }
`);

/**
 * The operation context that ties the chat's rules to urql's cache: the query
 * carries it, and so does a mutation that writes a rule, so the list is asked
 * again. A list holding no rule carries no KubeRule for the cache to key on,
 * so the type is named.
 */
export const chatGrantsContext = { additionalTypenames: ['KubeRule'] };

export type ChatGrant = ChatKubeRulesQuery['chatKubeRules'][number];

export type ChatGrants = {
  /** Undefined until the sidecar has answered. */
  rules: ChatGrant[] | undefined;
  /** The rule whose removal is in flight. */
  removing: string | null;
  /** The last refused removal's reason, until the next one starts. */
  error: string | null;
  remove: (id: string) => Promise<void>;
};

export function useChatGrants(chatID: string): ChatGrants {
  const [{ data }, reexecute] = useQuery({
    query: ChatKubeRulesDocument,
    variables: { chatID },
    context: chatGrantsContext,
    requestPolicy: 'cache-and-network',
  });
  const [, removeMutation] = useMutation(ChatKubeRuleRemoveMutation);
  const [removing, setRemoving] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const askAgain = useCallback(() => reexecute({ requestPolicy: 'network-only' }), [reexecute]);
  useEffect(() => {
    window.addEventListener('focus', askAgain);
    return () => window.removeEventListener('focus', askAgain);
  }, [askAgain]);

  const remove = useCallback(
    async (id: string) => {
      setRemoving(id);
      setError(null);
      const result = await removeMutation({ chatID, id });
      setRemoving(null);
      if (result.error) setError(result.error.graphQLErrors[0]?.message ?? result.error.message);
      askAgain();
    },
    [askAgain, chatID, removeMutation],
  );

  return { rules: data?.chatKubeRules, removing, error, remove };
}
