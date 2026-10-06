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

// The executables the sandbox probe checks, as the last probe found each, and the
// user's three changes: Probe again, register and remove. The one reader of
// the watch and the mutations. A probe can outlast a request, so the mutation
// starts one and answers at once, and the watch carries its end — a fresh
// value on each start, each end and each change to the registered executables,
// from every window, so nothing is asked again.
import { useCallback, useState } from 'react';

import { useMutation } from 'urql';

import { graphql } from '@/gql';
import type { SandboxExecutablesWatchSubscription } from '@/gql/graphql';
import { refusalOf } from '@/lib/graphql/refusal';
import { perFrame, useWatchSubscription } from '@/lib/graphql/use-watch-subscription';

const SandboxExecutablesWatchDocument = graphql(`
  subscription SandboxExecutablesWatch {
    sandboxExecutablesWatch {
      probing
      probes
      executables {
        name
        invocation
        registered
        probed
        resolved
        shim
        target
        ok
        version
        error
      }
    }
  }
`);

const SandboxExecutablesProbeMutation = graphql(`
  mutation SandboxExecutablesProbe {
    sandboxExecutablesProbe {
      name
    }
  }
`);

const SandboxExecutableRegisterMutation = graphql(`
  mutation SandboxExecutableRegister($name: String!, $invocation: String) {
    sandboxExecutableRegister(name: $name, invocation: $invocation) {
      name
    }
  }
`);

const SandboxExecutableRemoveMutation = graphql(`
  mutation SandboxExecutableRemove($name: String!) {
    sandboxExecutableRemove(name: $name) {
      name
    }
  }
`);

type SandboxExecutablesReport = SandboxExecutablesWatchSubscription['sandboxExecutablesWatch'];
export type SandboxExecutable = SandboxExecutablesReport['executables'][number];

// A gauge: the last frame is the whole state.
const lastReport = perFrame<SandboxExecutablesWatchSubscription, SandboxExecutablesReport>(
  (_, data) => data.sandboxExecutablesWatch,
);

export type SandboxExecutables = {
  /** Every executable the probe checks, as the last probe found each; undefined until the sidecar has answered. */
  report: SandboxExecutable[] | undefined;
  /** A probe runs, or has been asked for and not yet started. */
  probing: boolean;
  /** A register or a remove is in flight. */
  changing: boolean;
  /** Each mutation's last refusal, until it runs again. */
  probeError: string | null;
  registerError: string | null;
  removeError: string | null;
  probe: () => Promise<void>;
  /** Registers name with invocation, or `<name> --version` when it is empty; true once it is taken. */
  register: (name: string, invocation: string) => Promise<boolean>;
  remove: (name: string) => Promise<void>;
};

export function useSandboxExecutables(): SandboxExecutables {
  const { data } = useWatchSubscription({ query: SandboxExecutablesWatchDocument }, lastReport);
  const [, probeMutation] = useMutation(SandboxExecutablesProbeMutation);
  const [, registerMutation] = useMutation(SandboxExecutableRegisterMutation);
  const [, removeMutation] = useMutation(SandboxExecutableRemoveMutation);
  // The mutation's answer and the watch's frame arrive in no fixed order, and
  // the gauge can fold a short probe's start into its end, so from the press
  // the button is held down until the watch's count of probes moves past the
  // one seen at the press, or the mutation is refused.
  const [startedAt, setStartedAt] = useState<number | null>(null);
  const probes = data?.probes;
  const starting = startedAt !== null && (probes === undefined || probes <= startedAt);
  const [changing, setChanging] = useState(false);
  const [probeError, setProbeError] = useState<string | null>(null);
  const [registerError, setRegisterError] = useState<string | null>(null);
  const [removeError, setRemoveError] = useState<string | null>(null);

  const probe = useCallback(async () => {
    setStartedAt(probes ?? 0);
    const { error } = await probeMutation({});
    if (error) setStartedAt(null);
    setProbeError(refusalOf(error));
  }, [probeMutation, probes]);

  const register = useCallback(
    async (name: string, invocation: string) => {
      setChanging(true);
      const { error } = await registerMutation({ name, invocation: invocation === '' ? null : invocation });
      setChanging(false);
      setRegisterError(refusalOf(error));
      return !error;
    },
    [registerMutation],
  );

  const remove = useCallback(
    async (name: string) => {
      setChanging(true);
      const { error } = await removeMutation({ name });
      setChanging(false);
      setRemoveError(refusalOf(error));
    },
    [removeMutation],
  );

  return {
    report: data?.executables,
    probing: starting || (data?.probing ?? false),
    changing,
    probeError,
    registerError,
    removeError,
    probe,
    register,
    remove,
  };
}
