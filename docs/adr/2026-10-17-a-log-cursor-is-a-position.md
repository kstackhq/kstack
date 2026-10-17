---
title: A log cursor is a position, and a container's instance is its id
date: 2026-10-17
scope: sidecar
status: Accepted
---

# A log cursor is a position, and a container's instance is its id

## Context

The log viewer's wire (`sidecar/graph/schema.graphqls`; its edges in `src/lib/log-viewer/README.md`) has the sidecar own the
paging position: a page answers a `before` and an `after` edge, a follow sends checkpoints, and
the webview passes each cursor back unread. A plan worked out before the log cache, a note beside
that README, served it with no state between requests. Each page's edge cursor would carry base64
JSON with one entry per stream: where its log starts and how fast it writes, so the next backward
page sized its first read right, and how far its read reached, so a page or a follow going on
from the edge sent no line again and missed none.

A review found that plan's open problems were mostly the hint's. A hint denser than the log
could refuse a page. A stream that left the view and came back repeated from the follow's opening
moment. A skipped stream had no place to resume from. Every new key was one more thing
`parseCursor` had to check. The log cache (→ [the log cache](2026-10-17-the-log-cache.md)) then
gave the sidecar a place that already holds each stream's extent.

A cursor also had to name a line's stream, and it named it as the `StreamKey` does: namespace,
pod, container and `previous`. A line's key must not change, and `previous` does: the running
instance is previous after its container restarts.

## Decision

**A cursor is a position and nothing else** (`cursor.go`): a moment alone, or a moment with a
stream and an ordinal, written by `cursorOf` as tab-separated text and read back by
`parseCursor`. Nothing per stream rides in it. Where a stream's lines start, which ranges are
whole and where a tailer has reached live in the file, and `streamBound` reads the one cursor
as each stream's own bound.

**A stream in a position is named by its runtime instance**: namespace, pod and container name,
and the `container_id` of the instance that wrote the line, in the place `previous` held. Lines
order by time, then the names, then the ordinal, then the id. So lines at one moment keep their
order, and only two instances of one container at one moment sort by id. `parseCursor` takes any
id that is not empty, holds no control character and is at most 256 bytes, since a runtime
spells its ids as it likes.

This closes the note's plan for the cursor, and the note is deleted.

## Alternatives considered

- **Per-stream hints in the edge cursor**, the note's plan. It keeps the sidecar stateless
  between requests, at the price of a cursor that grows with the view and a list of hint edge
  cases. With the file holding each stream's extent, a backward page reads from the file and
  fills only its gaps, so the hint's saving is gone.
- **`previous` in the position.** It changes for one instance across a restart, so a line read
  as current and read again as previous would be two lines.
- **The file's stream id.** Shorter, but local to one file: a cache clear makes new ids, and a
  cursor the viewer holds would name a stream the new file does not have.
- **Base64 JSON.** It carried the hint's list. A position is a fixed handful of fields, and every
  field it carries is either a DNS name or checked to hold no control character, so a tab
  separates them safely.

## Consequences

A cursor stays short and fixed in size, and `parseCursor` checks a handful of fields. The note's
open problems about hints, a stream that leaves and returns, a skipped stream's history and the
cursor's checks no longer arise. Clock skew between the API server and a node is gone for a
stream the file has read, since its tailer resumes from its own last line's moment; a stream the
file has never read still starts from the follow's moment.

A `container_id` is longer than a name, which a cursor can afford, and it stays valid across a
cache clear. A position is only as good as the file behind it: once the file has evicted a range,
a page from a cursor inside it refills from the kubelet or crosses a lost gap.

An archive that knows a stream only by name stores it with an empty `container_id`, which
`parseCursor` refuses, so that backend brings a cursor change of its own.
