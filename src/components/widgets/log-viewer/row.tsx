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

// One log line as a row: its position from where the view landed, then the columns the user
// picked, as a grid. Every cell but the position and the dot is cluster text, so each goes
// through `VisibleText`. It is memoized over primitives, the line's own object and the column
// state's, so a frame that adds a line draws the new row alone, and a prepend that moves every
// row re-renders them without laying them out again.
import { type ReactNode, memo } from 'react';

import { cn } from '@kubetail/ui/lib/utils';

import { VisibleText } from '@/components/widgets/visible-text';
import type { LogLine } from '@/lib/log-viewer';
import { type Column, dotColor } from './columns';
import { POS_MIN_PX, type WidthKey } from './widths';

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

/** The grid every row and the header share: the position, a track per column, the message taking the rest. */
export const gridTemplate = (columns: Column[]) =>
  [`minmax(${POS_MIN_PX}px, auto)`, ...columns.map((col) => (col === 'Message' ? '1fr' : 'auto'))].join(' ');

/**
 * A cell's measured width as its `minWidth`. Rows are positioned apart, each its own grid, so
 * this is what lines their tracks up.
 */
export function cellStyle(widths: Map<WidthKey, number>, key: WidthKey) {
  const px = widths.get(key);
  return px ? { minWidth: px } : undefined;
}

function cellText(line: LogLine, col: Exclude<Column, 'Dot' | 'Message'>): string {
  switch (col) {
    case 'Timestamp':
      return lineTime(line.timestamp);
    case 'Pod':
      return line.stream.pod;
    case 'Container':
      return line.stream.container;
    case 'Node':
      return line.place.node;
    case 'Region':
      return line.place.region;
    case 'Zone':
      return line.place.zone;
    case 'OS':
      return line.place.os;
    default:
      return line.place.arch;
  }
}

// A container's colour, drawn inline so no CSS name is built from cluster text. The dot is
// the default set's only word on the stream, so a screen reader hears the names.
function Dot({ line }: { line: LogLine }) {
  const { namespace, pod, container } = line.stream;
  return (
    <span className="inline-flex items-center">
      <span
        aria-hidden
        className="inline-block size-2 rounded-full"
        style={{ backgroundColor: dotColor(namespace, pod, container) }}
      />
      <span className="sr-only">
        <VisibleText text={`${pod}/${container}`} />
      </span>
    </span>
  );
}

export type LogViewerRowProps = {
  line: LogLine;
  /** The core's key: 0 where the view landed, counting down before it and up after. */
  rowKey: number;
  columns: Column[];
  /** Each column's measured width, which every cell carries as `minWidth` so rows line up. */
  widths: Map<WidthKey, number>;
  /** The widest row measured: every row's `minWidth` while wrap is off. */
  rowWidth: number;
  wrap: boolean;
  /** Where the row sits under a scroller, from the top of its sizer. A row with none is in the flow. */
  top?: number;
  /** The core's index, for its `measureElement`. */
  index?: number;
  measureCell?: (el: HTMLElement | null) => void;
  measureRow?: (el: HTMLElement | null) => void;
  measureElement?: (el: Element | null) => void;
};

export const LogViewerRow = memo(
  ({
    line,
    rowKey,
    columns,
    widths,
    rowWidth,
    wrap,
    top,
    index,
    measureCell,
    measureRow,
    measureElement,
  }: LogViewerRowProps) => {
    const landed = rowKey === 0;

    const cells = columns.map((col) => {
      // A wrapped message takes what is left of the row and as many lines as it needs, so
      // neither its width nor the row's is measured. `min-w-0` lets its track shrink below an
      // unbroken token's width, which `break-word` alone does not.
      if (col === 'Message' && wrap) {
        return (
          <div key={col} data-col={col} className="min-w-0 px-2 whitespace-pre-wrap wrap-break-word">
            <VisibleText text={line.message} />
          </div>
        );
      }
      let content: ReactNode;
      if (col === 'Dot') content = <Dot line={line} />;
      else if (col === 'Message') content = <VisibleText text={line.message} />;
      else content = <VisibleText text={cellText(line, col)} />;
      return (
        <div
          key={col}
          ref={measureCell}
          data-col={col}
          className={cn('px-2', col === 'Message' ? 'whitespace-pre' : 'whitespace-nowrap text-muted-foreground')}
          style={cellStyle(widths, col)}
        >
          {/* The content is what is measured, so it is an inline-block of its own; the dot's already is. */}
          {col === 'Dot' ? content : <span className="inline-block">{content}</span>}
        </div>
      );
    });

    return (
      <div
        ref={(el) => {
          measureElement?.(el);
          if (!wrap) measureRow?.(el);
        }}
        data-testid="log-row"
        data-index={index}
        style={{
          gridTemplateColumns: gridTemplate(columns),
          minWidth: wrap ? '100%' : rowWidth || '100%',
          ...(top === undefined ? {} : { transform: `translateY(${top}px)` }),
        }}
        className={cn(
          'grid font-mono text-xs leading-5',
          wrap ? '' : LOG_ROW_H,
          top === undefined ? '' : 'absolute top-0 left-0',
        )}
      >
        {/* The sign is its own span, larger, so `+` and `-` read at a glance beside the digits.
            The landed row's marker sits here, its padding keeping the digits where the other rows' are. */}
        <div
          ref={measureCell}
          data-col="Pos"
          className={cn(
            'cursor-default pr-1.5 text-center text-[0.65rem] whitespace-nowrap tabular-nums select-none',
            landed ? 'border-l-2 border-primary pl-[7px] font-extrabold' : 'text-muted-foreground',
          )}
          style={cellStyle(widths, 'Pos')}
        >
          <span className="inline-block">
            {!landed && <span className="text-[0.9rem] text-muted-foreground">{rowKey > 0 ? '+' : '-'}</span>}
            {Math.abs(rowKey)}
          </span>
        </div>
        {cells}
      </div>
    );
  },
);
