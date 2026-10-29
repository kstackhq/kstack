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

import type { KubeClusterEntry } from '@/lib/kube-config';
import { tlsUnverifiedReason } from '@/lib/kube-config';

const UNVERIFIED_TLS_TITLE: Record<'skip-verify' | 'plain-http', string> = {
  'skip-verify': "The server's certificate is not checked: this context's cluster sets insecure-skip-tls-verify.",
  'plain-http': "The server's certificate is not checked: this context's server URL is plain http.",
};

// Rides beside the cluster it is about: the one way the app says a connection
// goes unverified, so it must not be conditioned on anything but the verdict.
export function UnverifiedTLSBadge({ entry }: { entry: KubeClusterEntry | null }) {
  const reason = tlsUnverifiedReason(entry);
  if (!reason) return null;
  return (
    <span
      className="inline-block shrink-0 rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-300"
      title={UNVERIFIED_TLS_TITLE[reason]}
    >
      Unverified TLS
    </span>
  );
}
