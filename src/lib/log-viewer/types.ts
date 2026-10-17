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

// The contract between the window and whatever feeds it. Each type mirrors the wire's
// (`LogRecord`, `LogRecordsPage`, `LogRecordsPageEdge`, `LogsFollowFrame`) field for field.

/** Opaque. The library hands a cursor back to the client that issued it and reads nothing from it. */
export type Cursor = string;

export type LogLine = {
  /** When the line was written: RFC 3339 in UTC (`Z`), with up to nine fraction digits. */
  timestamp: string;
  /** Names the line. A repeated line is dropped by it. */
  cursor: Cursor;
  /** The text. The library draws nothing and strips nothing: the client hands it text fit to show. */
  message: string;
  /** The log the line came from. Its four fields together name it. */
  stream: { namespace: string; pod: string; container: string; previous: boolean };
  /** Where the stream's pod runs. Empty where unknown, and no part of the stream's name. */
  place: { node: string; region: string; zone: string; os: string; arch: string };
  /**
   * Lines of the stream that may be missing just before this one, null when none are: the
   * stamps, RFC 3339, of the last known line before the hole and the first known line past it.
   */
  gap: { after: string; before: string } | null;
};

/** Where the window is told to be. `at` is the moment, RFC 3339, set for `At` alone. */
export type Anchor = { kind: 'Head' | 'Tail' | 'At'; at: string | null };

/**
 * `Head` reads oldest first after `cursor`, from the start with none; `Tail` reads newest first
 * before `cursor`, from the end with none; `Around` reads `limit` lines before `at` and `limit`
 * from it on, and takes `at` and no cursor.
 */
export type FetchMode = 'Head' | 'Tail' | 'Around';

export type FetchQuery = {
  mode: FetchMode;
  /** A page's edge cursor, a checkpoint's, or a line's. Null reads from the start or the end, and always for `Around`. */
  cursor: Cursor | null;
  /** For `Around` alone: the anchor's moment, RFC 3339. */
  at: string | null;
  limit: number;
};

/** One side of a page: where the next page that way continues, and whether there is one. Null on a `before` edge with nothing older. */
export type PageEdge = { cursor: Cursor | null; more: boolean };

export type Page = {
  /** Oldest first, as the client sorted them, whatever the mode. */
  lines: LogLine[];
  before: PageEdge;
  after: PageEdge;
  /** For `Around`: the index of the first line at or after the moment, `lines.length` with none. Null otherwise. */
  anchor: number | null;
  /** Streams the page could not read, one line of text each, for the viewer to show. */
  skipped: string[];
};

/** `Ended` is the last frame: the follow is over, and following again would end the same way. */
export type FollowFrame =
  | { kind: 'Line'; line: LogLine }
  | { kind: 'Checkpoint'; cursor: Cursor }
  | { kind: 'Ended'; reason: string };

/** Where lines come from. Anything that implements it can feed the window. */
export type Client = {
  fetch: (query: FetchQuery) => Promise<Page>;
  /**
   * Calls back per frame written after `after`, until the function it answers is called. A
   * follow that cannot open throws, or, when it learns so later, calls `onFailed` once and
   * sends nothing after.
   */
  follow: (after: Cursor, onFrame: (frame: FollowFrame) => void, onFailed: (error: unknown) => void) => () => void;
};
