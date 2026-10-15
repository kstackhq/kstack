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
// The contract between the viewer and whatever feeds it lines.

/**
 * Names a line's place in the log, so a client can be asked for the lines before
 * or after it. A timestamp today, and opaque to the viewer: it hands a cursor back
 * to the client that issued it and reads nothing from it.
 */
export type Cursor = string;

/** One log line. */
export type LogLine = {
  /** When the line was written, RFC 3339. */
  timestamp: string;
  cursor: Cursor;
  /** The text. The viewer draws nothing and strips nothing: the client hands it text fit to show. */
  message: string;
  /** The log the line came from. Its four fields together name it. */
  stream: {
    namespace: string;
    pod: string;
    container: string;
    /** Whether the log is the container's instance before its last restart. */
    previous: boolean;
  };
  /** Where the stream's pod runs. Empty where unknown, and no part of the stream's name. */
  place: {
    node: string;
    region: string;
    zone: string;
    os: string;
    arch: string;
  };
};

/** Where the viewer is told to be: the start, the end, or `At` a moment. `at` is that moment, RFC 3339, set for `At` alone. */
export type Anchor = { kind: 'Head' | 'Tail' | 'At'; at: string | null };

export type FetchOptions = {
  cursor?: Cursor | null;
  limit: number;
};

/**
 * A page of lines. `nextCursor` is set while there are more the way the page was
 * read, and is the last position the page scanned: the next page is `fetchAfter`
 * or `fetchBefore` it, which leave it out.
 */
export type FetchResult = {
  lines: LogLine[];
  nextCursor: Cursor | null;
  /** Streams the page could not read, one line of text each, for the viewer to show. */
  skipped: string[];
};

/**
 * Where lines come from. `since` and `until` keep the cursor's line, `after` and
 * `before` leave it out; `subscribe` calls back once per line written after `after`,
 * or after now when it is null, until the function it answers is called. A fetch
 * with no cursor reads from the log's start (`since`, `after`) or its end (`until`,
 * `before`).
 */
export type Client = {
  fetchSince: (options: FetchOptions) => Promise<FetchResult>;
  fetchUntil: (options: FetchOptions) => Promise<FetchResult>;
  fetchAfter: (options: FetchOptions) => Promise<FetchResult>;
  fetchBefore: (options: FetchOptions) => Promise<FetchResult>;
  subscribe: (after: Cursor | null, onLine: (line: LogLine) => void) => () => void;
};
