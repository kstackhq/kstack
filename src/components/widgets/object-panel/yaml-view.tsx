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

// The object panel's YAML tab: the focused object's body as the cache holds it, live, with
// Copy. It watches the object's kind itself, so it stays live whatever page the window
// moves to. A deleted object keeps its last body, marked Deleted.
import { Button } from '@kubetail/ui/elements/button';
import { Spinner } from '@kubetail/ui/elements/spinner';
import { Copy } from 'lucide-react';
import { useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';

import '@/components/widgets/chat/markdown.css';
import { VisibleText } from '@/components/widgets/visible-text';
import { copyToClipboard } from '@/lib/clipboard';
import { useClusterCachedDataObjects } from '@/lib/cluster-cached-data-objects';
import type { ClusterCachedDataObject } from '@/lib/cluster-cached-data-objects';
import { highlight, load, loaded } from '@/lib/highlight';
import type { FocusedObject } from '@/lib/object-view';
import { objectYaml } from '@/lib/object-yaml';

import { ObjectPanelNote } from './note';

type HastNode = ReturnType<typeof highlight>['children'][number];

// A highlighted tree as React elements, each run of text through VisibleText, since the
// YAML is the cluster's text.
function hastToReact(nodes: HastNode[]): ReactNode[] {
  return nodes.map((node, i) => {
    if (node.type === 'text') {
      // oxlint-disable-next-line react/no-array-index-key -- a tree's children are cut afresh and never reorder
      return <VisibleText key={i} text={node.value} />;
    }
    /* v8 ignore next -- highlight.js emits text and spans alone */
    if (node.type !== 'element') return null;
    // highlight.js gives every span its classes as a list.
    const className = node.properties.className as string[];
    return (
      // oxlint-disable-next-line react/no-array-index-key -- a tree's children are cut afresh and never reorder
      <span key={i} className={className.join(' ')}>
        {hastToReact(node.children)}
      </span>
    );
  });
}

// Plain until the YAML grammar has arrived, highlighted after.
function YamlText({ text }: { text: string }) {
  const [ready, setReady] = useState(() => loaded('yaml'));
  useEffect(() => {
    if (ready) return undefined;
    let live = true;
    void load('yaml').then((ok) => {
      if (live) setReady(ok);
    });
    return () => {
      live = false;
    };
  }, [ready]);
  const body = useMemo(
    () => (ready ? hastToReact(highlight('yaml', text).children) : <VisibleText text={text} />),
    [ready, text],
  );
  return <code>{body}</code>;
}

export function ObjectYamlView({ object }: { object: FocusedObject }) {
  // Its own connection: the kind's page may already hold the same watch, and a second
  // shared consumer would never see the snapshot.
  const { objects, active, phase } = useClusterCachedDataObjects(object, { unshared: true });
  const current = objects.find((o) => o.uid === object.uid);
  // The last body seen: held through a reconnect, and once the object is gone. The
  // sidebar keys the panel on the uid, so it never holds another object's.
  const [held, setHeld] = useState<ClusterCachedDataObject>();
  if (current && current !== held) setHeld(current);
  const shown = current ?? held;
  const deleted = phase === 'live' && !current;
  const yaml = useMemo(() => (shown ? objectYaml(shown.rawJSON) : ''), [shown]);

  if (!active) return <ObjectPanelNote>No synced cache for this cluster.</ObjectPanelNote>;
  if (!shown) {
    if (phase === 'live') return <ObjectPanelNote>This object is no longer in the cluster.</ObjectPanelNote>;
    return (
      <ObjectPanelNote>
        <Spinner size="sm" />
        Loading…
      </ObjectPanelNote>
    );
  }
  return (
    <>
      <div className="flex items-center gap-2 border-b py-1 pr-2 pl-4 text-xs text-muted-foreground">
        <span className="min-w-0 flex-1 truncate">
          {deleted ? (
            <span className="text-destructive">Deleted · its last version</span>
          ) : (
            'As cached: managed fields left out, Secret values redacted'
          )}
        </span>
        <Button
          variant="ghost"
          size="icon-sm"
          aria-label="Copy YAML"
          title="Copy YAML"
          onClick={() => copyToClipboard(yaml)}
        >
          <Copy aria-hidden />
        </Button>
      </div>
      <pre className="min-h-0 flex-1 overflow-auto p-4 font-mono text-xs">
        <YamlText text={yaml} />
      </pre>
    </>
  );
}
