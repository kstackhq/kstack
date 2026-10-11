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
// How a view is named in words, shared by the transcript's card and the right
// sidebar's header: the enum's word is the app's own, every name the cluster's.
import { VisibleText } from '@/components/widgets/visible-text';
import type { LogsViewAction } from '@/lib/logs-view';

type Source = LogsViewAction['sources'][number];
type Anchor = LogsViewAction['anchor'];

// Names from the cluster or the model, comma-separated, each spelled through
// VisibleText.
export function Names({ names }: { names: string[] }) {
  return names.map((name, i) => (
    // eslint-disable-next-line react/no-array-index-key
    <span key={i}>
      {i > 0 && ', '}
      <VisibleText text={name} />
    </span>
  ));
}

// `Deployment webapp in prod (app, istio-proxy), previous instance`.
export function SourceLine({ source }: { source: Source }) {
  return (
    <>
      {source.kind} <VisibleText text={source.name} /> in <VisibleText text={source.namespace} />
      {source.containers.length > 0 && (
        <>
          {' ('}
          <Names names={source.containers} />)
        </>
      )}
      {source.previous && ', previous instance'}
    </>
  );
}

// The moment is drawn as the backend stamped it.
export function anchorLine(anchor: Anchor): string {
  if (anchor.kind === 'Head') return 'from the start';
  if (anchor.kind === 'Tail') return 'at the newest line';
  return `from ${anchor.at ?? ''}`;
}
