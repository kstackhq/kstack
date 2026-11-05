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

// What the sidecar can answer with. A query, not a watch: the set is fixed for the
// sidecar's life while keys come from the environment the host holds. The one reader
// of it, since the next feature's picker reads it too.
import { useCallback, useMemo } from 'react';
import { useQuery } from 'urql';

import { graphql } from '@/gql';
import type { ModelsQuery } from '@/gql/graphql';

const ModelsDocument = graphql(`
  query Models {
    models {
      provider {
        id
        label
      }
      id
      label
      efforts
      defaultEffort
    }
  }
`);

export type Model = ModelsQuery['models'][number];

/** One model, as a send names it: the provider's id and its own. */
export type ModelRef = { providerID: string; id: string };

/**
 * The catalog. `loaded` is a sidecar that answered — with nothing, when it has no
 * key, which disables Send with a placeholder. `failed` is one that could not be
 * reached: nothing was answered, and `retry` is the only way back, since a query
 * re-runs for nobody.
 */
export type Catalog = { models: Model[]; loaded: boolean; failed: boolean; retry: () => void };

const NONE: Model[] = [];

export function useModels(): Catalog {
  const [{ data, error, fetching }, reexecute] = useQuery({ query: ModelsDocument });
  const models = data?.models;
  // Past the cache: what failed is what we are asking again for.
  const retry = useCallback(() => reexecute({ requestPolicy: 'network-only' }), [reexecute]);
  return useMemo(
    () => ({ models: models ?? NONE, loaded: !fetching && !error, failed: !fetching && error !== undefined, retry }),
    [models, error, fetching, retry],
  );
}

/** What a send runs on: a model the catalog holds, and one of its effort levels. */
export type ModelPick = { model: ModelRef; effort: string };

/**
 * What a chat's composer starts on: the chat's last answer, or the catalog's first
 * model when there is none or the catalog no longer offers it. Undefined for an
 * empty catalog, which is nothing to pick from.
 */
export function seedPick(models: Model[], lastAnswer: (ModelRef & { effort: string }) | null): ModelPick | undefined {
  // The effort belongs to the model that ran it: another model spelling the same
  // level is not the same level, and carrying it over would quietly change what a
  // turn costs.
  const answered = modelOf(models, lastAnswer);
  const model = answered ?? models[0];
  if (!model) return undefined;
  const effort =
    answered && model.efforts.includes(lastAnswer?.effort ?? '') ? lastAnswer!.effort : model.defaultEffort;
  return { model: { providerID: model.provider.id, id: model.id }, effort };
}

/** The catalog's entry for a ref, or null when it no longer offers it. */
export function modelOf(models: Model[], ref: ModelRef | null): Model | null {
  if (!ref) return null;
  return models.find((m) => m.provider.id === ref.providerID && m.id === ref.id) ?? null;
}
