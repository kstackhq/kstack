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

// The permission modes and rules: the one reader of the `permissionSettings`
// query and the five mutations over modes and always rules, for the Settings
// section.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery } from 'urql';
import type { CombinedError } from 'urql';

import { graphql } from '@/gql';
import type { PermissionClass, PermissionEffect, PermissionMode, PermissionSettingsQuery } from '@/gql/graphql';
import { refusalOf, savedUnread } from '@/lib/graphql/refusal';

const PermissionSettingsDocument = graphql(`
  query PermissionSettings {
    permissionSettings {
      defaultMode
      clusters {
        clusterID
        mode
        source
      }
      kubeRules {
        id
        effect
        class
        clusterID
        namespace
        verb
        group
        kind
        line
      }
      destructive
    }
  }
`);

const DefaultModeSetMutation = graphql(`
  mutation PermissionDefaultModeSet($mode: PermissionMode!) {
    permissionDefaultModeSet(mode: $mode) {
      defaultMode
    }
  }
`);

const ModeSetMutation = graphql(`
  mutation PermissionModeSet($clusterID: ClusterID!, $mode: PermissionMode!) {
    permissionModeSet(clusterID: $clusterID, mode: $mode) {
      defaultMode
    }
  }
`);

const ModeClearMutation = graphql(`
  mutation PermissionModeClear($clusterID: ClusterID!) {
    permissionModeClear(clusterID: $clusterID) {
      defaultMode
    }
  }
`);

const KubeRuleAddMutation = graphql(`
  mutation KubeRuleAdd($input: KubeRuleInput!) {
    kubeRuleAdd(input: $input) {
      defaultMode
    }
  }
`);

const KubeRuleRemoveMutation = graphql(`
  mutation KubeRuleRemove($id: String!) {
    kubeRuleRemove(id: $id) {
      defaultMode
    }
  }
`);

export type PermissionSettingsValue = PermissionSettingsQuery['permissionSettings'];

export type ClusterMode = PermissionSettingsValue['clusters'][number];

export type RuleInput = {
  effect: PermissionEffect;
  class: PermissionClass;
  /** The one cluster the rule applies to; null is every cluster, which only Deny and Ask may name. */
  clusterID: string | null;
  namespace: string;
  verb: string;
  group: string;
  kind: string;
};

export type PermissionSettings = {
  /** Undefined until the sidecar has answered. */
  settings: PermissionSettingsValue | undefined;
  /** Why the settings could not be read, while they cannot. */
  readError: string | null;
  /** Asks the sidecar for the settings again. */
  retry: () => void;
  /** The last mutation's refusal, until the next mutation. */
  error: string | null;
  setDefaultMode: (mode: PermissionMode) => void;
  setMode: (clusterID: string, mode: PermissionMode) => void;
  clearMode: (clusterID: string) => void;
  /** Resolves to whether the sidecar took the rule. */
  addRule: (input: RuleInput) => Promise<boolean>;
  removeRule: (id: string) => void;
};

export function usePermissionSettings(): PermissionSettings {
  // Asked again on every opening and whenever the window regains focus: another
  // window can change the settings, and the clusters watch can learn a cluster,
  // and neither reaches this window's cache.
  const [{ data, error: queryError }, reexecute] = useQuery({
    query: PermissionSettingsDocument,
    requestPolicy: 'cache-and-network',
  });
  const refresh = useCallback(() => reexecute({ requestPolicy: 'network-only' }), [reexecute]);
  useEffect(() => {
    window.addEventListener('focus', refresh);
    return () => window.removeEventListener('focus', refresh);
  }, [refresh]);
  const [error, setError] = useState<string | null>(null);
  const [, setDefaultMode] = useMutation(DefaultModeSetMutation);
  const [, setMode] = useMutation(ModeSetMutation);
  const [, clearMode] = useMutation(ModeClearMutation);
  const [, addRule] = useMutation(KubeRuleAddMutation);
  const [, removeRule] = useMutation(KubeRuleRemoveMutation);

  const settings = data?.permissionSettings;
  const readError = refusalOf(queryError);
  return useMemo(() => {
    // Every mutation answers the settings it left, so urql's cache asks the
    // query again; a refusal is drawn until the next mutation. A change that
    // took though its answer could not be read is taken, and says so.
    const run = async (mutation: Promise<{ error?: CombinedError }>) => {
      const answer = await mutation;
      const refusal = refusalOf(answer.error);
      setError(refusal);
      return refusal === null || savedUnread(answer.error);
    };
    return {
      settings,
      readError,
      retry: refresh,
      error,
      setDefaultMode: (mode) => run(setDefaultMode({ mode })),
      setMode: (clusterID, mode) => run(setMode({ clusterID, mode })),
      clearMode: (clusterID) => run(clearMode({ clusterID })),
      addRule: (input) => run(addRule({ input })),
      removeRule: (id) => run(removeRule({ id })),
    };
  }, [settings, readError, refresh, error, setDefaultMode, setMode, clearMode, addRule, removeRule]);
}
