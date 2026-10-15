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

// One log line as a row: the time, the pod and the message, in mono on one line. Every
// cell is cluster text, so each goes through `VisibleText`. It takes a line and nothing of
// the scroller's, so a card can draw the same row with no core around it. It is memoized
// over primitives and the line's own object, so a frame that adds a line draws the new row
// alone, and a prepend that moves every row re-renders them without laying them out again.
import { memo } from 'react';

import { cn } from '@kubetail/ui/lib/utils';

import { VisibleText } from '@/components/widgets/visible-text';
import type { LogLine } from '@/lib/log-viewer';

/** The class every row and edge row carries. */
export const LOG_ROW_H = 'h-5';
/** `LOG_ROW_H` in pixels, read off a rendered row: jsdom has no layout to check it against. */
export const LOG_ROW_PX = 20;

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

const pad = (n: number, width = 2) => String(n).padStart(width, '0');

// Local time, with the date before it for a line not from today. The wire allows nine
// fraction digits, and how many `Date` parses is the engine's choice, so it gets three.
export function lineTime(timestamp: string): string {
  const at = new Date(timestamp.replace(/(\.\d{3})\d+/, '$1'));
  const now = new Date();
  const time = `${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}.${pad(at.getMilliseconds(), 3)}`;
  const today =
    at.getFullYear() === now.getFullYear() && at.getMonth() === now.getMonth() && at.getDate() === now.getDate();
  return today ? time : `${MONTHS[at.getMonth()]} ${at.getDate()} ${time}`;
}

export const LogViewerRow = memo(
  ({
    line,
    showContainer,
    landed,
    top,
  }: {
    line: LogLine;
    /** Whether the view reads more than one container, so the pod alone does not name the stream. */
    showContainer: boolean;
    /** Whether this is the line the view landed on. */
    landed: boolean;
    /** Where the row sits under a scroller, from the top of its sizer. A row with none is in the flow. */
    top?: number;
  }) => {
    const { pod, container } = line.stream;
    return (
      <div
        data-testid="log-row"
        style={top === undefined ? undefined : { transform: `translateY(${top}px)` }}
        className={cn(
          LOG_ROW_H,
          'flex min-w-full w-max items-center gap-3 px-3 font-mono text-xs leading-5 whitespace-pre',
          top === undefined ? '' : 'absolute top-0 left-0',
          landed ? 'border-l-2 border-primary pl-2.5' : '',
        )}
      >
        <span className="text-muted-foreground">
          <VisibleText text={lineTime(line.timestamp)} />
        </span>
        <span className="text-muted-foreground">
          <VisibleText text={showContainer ? `${pod}/${container}` : pod} />
        </span>
        <span>
          <VisibleText text={line.message} />
        </span>
      </div>
    );
  },
);
