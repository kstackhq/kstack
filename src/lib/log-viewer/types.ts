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

/** How the client asks for the lines before or after one. A line's timestamp today. */
export type Cursor = string;

/** The wire's `LogRecord`, plus the cursor the client sends back for it. */
export type LogLine = {
  /** RFC 3339, as the sidecar hands it back. */
  timestamp: string;
  cursor: Cursor;
  /** Text, with terminal control sequences stripped by the sidecar. */
  message: string;
  source: {
    namespace: string;
    pod: string;
    container: string;
    previous: boolean;
    node: string;
    region: string;
    zone: string;
    os: string;
    arch: string;
  };
};

/** Where the viewer is told to be: the wire's `LogsViewAnchor`. `at` is set for `At` alone. */
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
  /** Streams the page could not read, one line each, in the sidecar's words. */
  skipped: string[];
};

/**
 * Where lines come from. `since` and `until` keep the cursor's line, `after` and
 * `before` leave it out; `subscribe` calls back once per line written after `after`,
 * or after now when it is null, until the function it answers is called.
 */
export type Client = {
  fetchSince: (options: FetchOptions) => Promise<FetchResult>;
  fetchUntil: (options: FetchOptions) => Promise<FetchResult>;
  fetchAfter: (options: FetchOptions) => Promise<FetchResult>;
  fetchBefore: (options: FetchOptions) => Promise<FetchResult>;
  subscribe: (after: Cursor | null, onLine: (line: LogLine) => void) => () => void;
};
