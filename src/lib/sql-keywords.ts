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

const KEYWORDS = new Set([
  'ALL',
  'AND',
  'AS',
  'ASC',
  'BETWEEN',
  'BY',
  'CASE',
  'CROSS',
  'DESC',
  'DISTINCT',
  'ELSE',
  'END',
  'EXCEPT',
  'EXISTS',
  'FROM',
  'GLOB',
  'GROUP',
  'HAVING',
  'IN',
  'INNER',
  'INTERSECT',
  'IS',
  'JOIN',
  'LEFT',
  'LIKE',
  'LIMIT',
  'NOT',
  'NULL',
  'OFFSET',
  'ON',
  'OR',
  'ORDER',
  'OUTER',
  'SELECT',
  'THEN',
  'UNION',
  'USING',
  'WHEN',
  'WHERE',
  'WITH',
]);

// A quoted string or identifier, a line comment, or a word. What a keyword can
// hide in comes first, so a word inside quotes is never read as one.
const TOKEN = /'(?:[^']|'')*'?|"(?:[^"]|"")*"?|--[^\n]*|[A-Za-z_][A-Za-z0-9_]*/g;

/** A run of a statement: its text, and whether it is an SQL keyword. */
export type SqlRun = { text: string; keyword: boolean };

/** `sql` cut into runs, each keyword its own; the runs join back to `sql`. */
export function sqlRuns(sql: string): SqlRun[] {
  const runs: SqlRun[] = [];
  let end = 0;
  for (const { 0: word, index } of sql.matchAll(TOKEN)) {
    if (!KEYWORDS.has(word.toUpperCase())) continue;
    if (index > end) runs.push({ text: sql.slice(end, index), keyword: false });
    runs.push({ text: word, keyword: true });
    end = index + word.length;
  }
  if (end < sql.length) runs.push({ text: sql.slice(end), keyword: false });
  return runs;
}
