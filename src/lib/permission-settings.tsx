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
// and `securityRefused` queries and the six `permission*` mutations, for the
// Settings section and the onboarding flow.
import { useEffect, useMemo, useState } from 'react';

import { useMutation, useQuery } from 'urql';
import type { CombinedError } from 'urql';

import { graphql } from '@/gql';
import type { PermissionClass, PermissionEffect, PermissionMode, PermissionSettingsQuery } from '@/gql/graphql';
import { refusalOf } from '@/lib/graphql/refusal';

const PermissionSettingsDocument = graphql(`
  query PermissionSettings {
    permissionSettings {
      defaultMode
      contexts {
        context
        mode
        source
        pattern
        own
      }
      rules {
        id
        line
      }
      destructive
      held
    }
  }
`);

const SecurityRefusedQuery = graphql(`
  query SecurityRefused {
    securityRefused {
      field
      value
      reason
    }
  }
`);

const DefaultModeSetMutation = graphql(`
  mutation PermissionDefaultModeSet($mode: PermissionMode!) {
    permissionDefaultModeSet(mode: $mode) {
      held
    }
  }
`);

const ModeSetMutation = graphql(`
  mutation PermissionModeSet($context: String!, $mode: PermissionMode!) {
    permissionModeSet(context: $context, mode: $mode) {
      held
    }
  }
`);

const ModeClearMutation = graphql(`
  mutation PermissionModeClear($context: String!) {
    permissionModeClear(context: $context) {
      held
    }
  }
`);

const RuleAddMutation = graphql(`
  mutation PermissionRuleAdd($input: PermissionRuleInput!) {
    permissionRuleAdd(input: $input) {
      held
    }
  }
`);

const RuleRemoveMutation = graphql(`
  mutation PermissionRuleRemove($id: String!) {
    permissionRuleRemove(id: $id) {
      held
    }
  }
`);

const DiscardRefusedMutation = graphql(`
  mutation PermissionDiscardRefused($field: String!) {
    permissionDiscardRefused(field: $field) {
      held
    }
  }
`);

export type PermissionSettingsValue = PermissionSettingsQuery['permissionSettings'];

export type Refusal = { field: string; value: string; reason: string };

export type RuleInput = {
  effect: PermissionEffect;
  class: PermissionClass;
  context: string;
  namespace: string;
  verb: string;
  group: string;
  kind: string;
};

export type PermissionSettings = {
  /** Undefined until the sidecar has answered. */
  settings: PermissionSettingsValue | undefined;
  refused: Refusal[];
  /** The last mutation's refusal, until the next mutation. */
  error: string | null;
  /** Whether the file holds a value of field Kstack cannot read. */
  held: (field: string) => boolean;
  setDefaultMode: (mode: PermissionMode) => void;
  setMode: (context: string, mode: PermissionMode) => void;
  clearMode: (context: string) => void;
  addRule: (input: RuleInput) => void;
  removeRule: (id: string) => void;
  discardRefused: (field: string) => void;
};

export function usePermissionSettings(): PermissionSettings {
  // Asked again on every opening and whenever the window regains focus: another
  // window can change the settings, and the clusters watch can learn a context,
  // and neither reaches this window's cache.
  const [{ data }, reexecute] = useQuery({ query: PermissionSettingsDocument, requestPolicy: 'cache-and-network' });
  useEffect(() => {
    const refresh = () => reexecute({ requestPolicy: 'network-only' });
    window.addEventListener('focus', refresh);
    return () => window.removeEventListener('focus', refresh);
  }, [reexecute]);
  const [{ data: refusedData }] = useQuery({ query: SecurityRefusedQuery });
  const [error, setError] = useState<string | null>(null);
  const [, setDefaultMode] = useMutation(DefaultModeSetMutation);
  const [, setMode] = useMutation(ModeSetMutation);
  const [, clearMode] = useMutation(ModeClearMutation);
  const [, addRule] = useMutation(RuleAddMutation);
  const [, removeRule] = useMutation(RuleRemoveMutation);
  const [, discardRefused] = useMutation(DiscardRefusedMutation);

  const settings = data?.permissionSettings;
  const refused = refusedData?.securityRefused;
  return useMemo(() => {
    // Every mutation answers the settings it left, so urql's cache asks the
    // query again; a refusal is drawn until the next mutation.
    const run = async (mutation: Promise<{ error?: CombinedError }>) => {
      setError(refusalOf((await mutation).error));
    };
    return {
      settings,
      refused: refused ?? [],
      error,
      held: (field) => settings?.held.includes(field) ?? false,
      setDefaultMode: (mode) => run(setDefaultMode({ mode })),
      setMode: (context, mode) => run(setMode({ context, mode })),
      clearMode: (context) => run(clearMode({ context })),
      addRule: (input) => run(addRule({ input })),
      removeRule: (id) => run(removeRule({ id })),
      discardRefused: (field) => run(discardRefused({ field })),
    };
  }, [settings, refused, error, setDefaultMode, setMode, clearMode, addRule, removeRule, discardRefused]);
}
