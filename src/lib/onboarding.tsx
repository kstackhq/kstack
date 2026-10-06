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

// The onboarding flow's state: whether it has been finished on this machine,
// the launcher that opens it, and the write that finishes it. The flag gates
// nothing; it only decides whether the flow opens at launch.
import { useCallback, useEffect, useRef, useState } from 'react';

import { useMutation, useQuery } from 'urql';

import { graphql } from '@/gql';
import { useDialog } from '@/lib/dialog';
import { refusalOf } from '@/lib/graphql/refusal';
import { useSandbox } from '@/lib/sandbox';

const OnboardingQuery = graphql(`
  query Onboarding {
    onboarding {
      finished
    }
  }
`);

const OnboardingFinishMutation = graphql(`
  mutation OnboardingFinish {
    onboardingFinish {
      finished
    }
  }
`);

export type OnboardingFinish = {
  /** The finish is in flight. */
  finishing: boolean;
  /** The last refused finish's message, until the next one. */
  finishError: string | null;
  /** True once the flag is written. */
  finish: () => Promise<boolean>;
};

export function useOnboardingFinish(): OnboardingFinish {
  const [, finishMutation] = useMutation(OnboardingFinishMutation);
  const [finishing, setFinishing] = useState(false);
  const [finishError, setFinishError] = useState<string | null>(null);
  const finish = useCallback(async () => {
    setFinishing(true);
    const { error } = await finishMutation({});
    setFinishing(false);
    setFinishError(refusalOf(error));
    return !error;
  }, [finishMutation]);
  return { finishing, finishError, finish };
}

/**
 * Opens the onboarding dialog once per webview, when the flow is not finished
 * and the sandbox query has answered. `available` is undefined before it
 * answers and after a failure, and a failure read as "no sandbox" would draw
 * the one screen whose OK finishes the flow, so either query failing opens
 * nothing and the next launch asks again.
 */
export function useOnboardingLaunch() {
  const [{ data }] = useQuery({ query: OnboardingQuery });
  const { available } = useSandbox();
  const { openDialog } = useDialog();
  // A dialog the user closed is not reopened by a re-render.
  const opened = useRef(false);
  const finished = data?.onboarding.finished;
  useEffect(() => {
    if (opened.current || finished !== false || available === undefined) return;
    opened.current = true;
    openDialog('onboarding');
  }, [finished, available, openDialog]);
}

/** Mounts the launcher; it must sit under `DialogProvider`. */
export function OnboardingLaunch() {
  useOnboardingLaunch();
  return null;
}
