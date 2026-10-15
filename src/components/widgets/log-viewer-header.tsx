// Copyright 2026 The Kstack Authors
// Copyright 2024 The Kubetail Authors
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

// The column names over the log viewer's rows, in the rows' grid. It sits outside the scroller,
// so it follows the scroller sideways, and it reserves the scroller's vertical scrollbar at its
// right edge so the names stay over their columns at the far right.
import { useEffect, useRef, useState } from 'react';

import type { Column } from '@/components/widgets/log-viewer-columns';
import { cellStyle, gridTemplate } from '@/components/widgets/log-viewer-row';
import type { WidthKey } from '@/components/widgets/log-viewer-widths';

// The position and the dot draw no name; a screen reader still gets one. `Container` names a
// column of its own, so the dot's is the source.
const HIDDEN_NAMES: Partial<Record<WidthKey, string>> = { Pos: 'Position', Dot: 'Source' };

function Name({ col }: { col: WidthKey }) {
  const hidden = HIDDEN_NAMES[col];
  return hidden ? <span className="sr-only">{hidden}</span> : <span className="inline-block">{col}</span>;
}

export function LogViewerHeader({
  columns,
  widths,
  rowWidth,
  wrap,
  scroller,
  measureCell,
}: {
  columns: Column[];
  widths: Map<WidthKey, number>;
  rowWidth: number;
  wrap: boolean;
  /** The core's scroller, once a landing has mounted one. */
  scroller: HTMLElement | null;
  measureCell?: (el: HTMLElement | null) => void;
}) {
  const wrapper = useRef<HTMLDivElement>(null);
  const [gutter, setGutter] = useState(0);

  // Hidden overflow takes a `scrollLeft` from code and none from the user, so a swipe over the
  // header cannot move it out of line with the rows.
  useEffect(() => {
    const header = wrapper.current;
    if (!scroller || !header) return undefined;
    const follow = () => {
      header.scrollLeft = scroller.scrollLeft;
    };
    follow();
    scroller.addEventListener('scroll', follow, { passive: true });
    return () => scroller.removeEventListener('scroll', follow);
  }, [scroller]);

  useEffect(() => {
    if (!scroller) return undefined;
    const observer = new ResizeObserver(() => setGutter(scroller.offsetWidth - scroller.clientWidth));
    observer.observe(scroller);
    return () => observer.disconnect();
  }, [scroller]);

  return (
    <div ref={wrapper} className="shrink-0 overflow-x-hidden border-b bg-muted">
      <div className="flex">
        <div
          data-testid="log-header"
          className="grid shrink-0 text-[0.65rem] leading-5 tracking-wide text-muted-foreground uppercase select-none"
          style={{ gridTemplateColumns: gridTemplate(columns), minWidth: wrap ? '100%' : rowWidth || '100%' }}
        >
          {(['Pos', ...columns] as WidthKey[]).map((col) => (
            <div
              key={col}
              ref={col === 'Message' && wrap ? undefined : measureCell}
              data-col={col}
              className="px-2 whitespace-nowrap"
              style={col === 'Message' && wrap ? undefined : cellStyle(widths, col)}
            >
              <Name col={col} />
            </div>
          ))}
        </div>
        {gutter > 0 && (
          <div data-testid="log-header-gutter" aria-hidden className="shrink-0" style={{ width: gutter }} />
        )}
      </div>
    </div>
  );
}
