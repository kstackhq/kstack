# Step 2: the namespace scope and the page frame

Add a namespace scope to the window, picked at the top of the sidebar, and the header every
dashboard page shares. The series and its rules are in [`README.md`](README.md).

Frames, on the _K8s Dashboard (imported)_ page of the Figma file:

- `K8s/ScopeSwitcher`, `Style=Solid`: the namespace picker's button.
- `Kind switcher menu (open)`: the look of the picker's menu (search box, list, footer).
- The _Header_ of `Pods (filtered to checkout)`, `Events (filtered to checkout)` and
  `PersistentVolumes (cluster-scoped)`. The rest of those frames is steps 3 to 5.

No frame draws the namespace picker's menu; this step settles it.

**This step departs from the frames' page toolbar.** The frames put the namespace switcher and a
kind switcher in a breadcrumb above each page. Here the namespace picker is the sidebar's first
row, beside the counts and sections it changes, and there is no kind switcher: every row it
would list is a sidebar row. A page starts with its header.

## What the step settles

**The scope is one namespace or all of them.** It is a `namespace` search param on `_app`,
beside `kubeContext`, `resource` and `chat`, and `retainSearchParams` keeps it across the
switch to chat and back. Absent means all namespaces. Like `resource`, it is validated
leniently: any non-empty string. A new ADR records it, amending
[the cluster is the app's scope](../../adr/2026-09-11-the-cluster-is-the-apps-scope.md).

**A cluster switch clears the scope**, as it clears `chat`: the picker's navigation writes
`namespace: undefined`. A namespace belongs to a cluster.

**Only the dashboard reads the scope.** Chat mode ignores it, and no chat send carries it.

**Each dashboard page draws a header**, then the panel it draws today. The header replaces
today's `<h1>`.

### The namespace picker

The first row of the dashboard's sidebar, under the Chat/Dashboard switch and above Overview and
Events, as wide as the nav. It shows a layers icon, _All namespaces_ or the namespace's name
through `VisibleText`, and a chevron. It opens a menu:

- a search box, _Filter namespaces…_, matching a case-insensitive substring of the name;
- _All namespaces_, always first and never filtered out;
- the cluster's namespaces by name;
- the current one marked, and, if the cluster does not list it, still shown at the top with
  _Not found_ beside it;
- a footer: _↑↓ navigate_, _↵ open_, _esc close_.

↑ and ↓ move the highlight over rows, skipping labels; Enter picks the highlighted row; Escape
closes. Focus stays in the search box while the arrows move the highlight. The list opens
scrolled to the current row. Picking a row writes `namespace` and closes the menu.

The list comes from the cache's `v1/namespaces` objects. Before their `Bookmark` it reads
_Loading namespaces…_; with no active cache, or a cache that does not serve namespaces, it holds
_All namespaces_ and the current one alone.

The menu is `ScopeMenu` and its button `ScopeTrigger`, in `components/widgets/dashboard-resource-nav/`.
The menu takes rows and labels and a function deciding what a query matches, and owns the search
box, the keyboard, the scroll-to-current and the footer.

### The header

**A kind's page:** its label as the title, its object count, then its `apiVersion` and short
names in monospace (`v1 · po`). The short names are a static table on the curated kinds in
`GROUPS`. Other kinds draw the `apiVersion` alone, since the catalog carries no short names
(_Data gaps_).

**The count is the rows the page shows.** It is drawn only once the kind's objects have their
`Bookmark`. To keep the two the same, the page reads the objects watch and hands the rows to
`ObjectsTable`, which no longer subscribes itself.

**A cluster-scoped kind under a namespace** reads, under the title, _Cluster-scoped, so the
`<namespace>` filter isn't applied._ The picker keeps the namespace: it applies again on the
next namespaced page.

**Events:** the title _Events_ and its count, with _Events in `<namespace>`._ under it while a
namespace is chosen.

**Overview:** the title _Overview_. Its body stays _coming soon_ until step 5.

### The scope applies now

A picker that changed nothing would be a dead control, so this step filters the pages that
exist, client-side:

- `ObjectsTable` of a namespaced kind shows the scope's objects alone, and drops its Namespace
  column while a namespace is chosen.
- `EventsTable` shows the events whose `involvedNamespace` is the scope.
- A filtered table with no rows reads _No `<kind>` in `<namespace>`_.

The watches stay cluster-wide; the sidecar serves no namespaced watch.

**The sidebar follows the scope.** The _Namespace_ section's counts are the namespace's
(below). A kind empty in the namespace hides, like one empty in the cluster. Its heading stays
_Namespace_: the picker above it names the namespace.

### The catalog counts in the namespace

The sidecar's one change in the series. `clusterCachedDataKindsWatch` takes an optional
`namespace`, and each frame echoes it (`""` without one), as provenance beside `cacheID`:

- a namespaced kind counts its objects in the namespace, through the `objects_ns_kind` index;
- the Events kind counts the events whose involved object is in the namespace;
- a cluster-scoped kind keeps its whole count.

No schema changes: `kubestore`'s `KindsIn` counts on each read, so a scoped read costs the
namespace's objects rather than `O(kinds)`. `ClusterCachedDataKindsProvider` subscribes with
the scope, so a namespace switch re-subscribes, as a cache swap does.

### One namespaces watch

urql shares one connection per query and variables, so a consumer that mounts into a running
watch never sees its snapshot (`src/CLAUDE.md`, _Connection lifecycle_). The namespace list and
a Namespaces page would both watch `v1/namespaces`. The namespace picker's watch is held in a
`ClusterNamespacesProvider` beside `ClusterCachedDataKindsProvider` in `AppLayout`, enabled only
on the dashboard, and subscribes `unshared`. `useClusterCachedDataObjects` gains the
`unshared` and `enabled` options to allow it.

## Not built

- _Ask_. Without the page in a chat's context, it would only repeat the right sidebar's toggle.
- The Events page's _Live_ toggle and _Summarize these events_ (assistant-written).
- The page toolbar and the kind switcher (above, *This step departs*). A keyboard jump to any page
  is in [`docs/TODO.md`](../../TODO.md#frontend-webview).
- The tooltip on the namespace picker over a cluster-scoped page. The header's line says it.
- Saying the scope while the sidebar is collapsed, and so the picker hidden.
- More than one namespace in the scope ([`docs/TODO.md`](../../TODO.md#frontend-webview)).
- _Events … plus cluster-wide events that affect it_: it needs to know which cluster-scoped
  objects a namespace depends on.

## Tasks

| #   | Task                                                                                                                                         | Status  |
| --- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| 1   | The ADR; `namespace` on `_app` with `retainSearchParams`; `useNamespaceScope` reading and writing it; the cluster picker clearing it          | Done    |
| 2   | `unshared` on `useClusterCachedDataObjects`; `ClusterNamespacesProvider` in `AppLayout`                                                       | Done    |
| 3   | `ScopeMenu`: search, rows and labels, keyboard, scroll-to-current, footer, light and dark                                                    | Done    |
| 4   | The namespace picker over `ScopeMenu`, first in the sidebar; the short names on `GROUPS`                                                    | Done    |
| 5   | The header in `routes/dashboard.tsx`; the objects watch lifted out of `ObjectsTable`                                                         | Done    |
| 6   | The scope applied: `ObjectsTable`, `EventsTable`, the sidebar's heading and counts                                                           | Done    |
| 7   | `src/CLAUDE.md`: _Routing & layout_ (the param), _Dashboard resource nav_ (the heading, counts), the page frame; `docs/TODO.md` for the gaps | Done    |
| 8   | `kubestore.KindsIn`; `namespace` on `WatchKinds` and `clusterCachedDataKindsWatch`, echoed on each frame; the provider subscribes with it | Done    |

## Tests

- `_app.test.tsx`: `namespace` validated and kept across a mode switch; absent reads as all.
- The cluster picker: a switch clears `namespace` along with `chat`.
- `scope-menu.test.tsx`: the search filters rows and keeps the always-shown ones; ↑↓ skip
  labels and wrap at neither end; Enter picks; Escape closes; it opens scrolled to the current
  row; rows draw through `VisibleText`.
- The namespace picker: _All namespaces_ first; namespaces sorted; _Loading namespaces…_
  before the `Bookmark`; a missing current namespace marked _Not found_; a pick writes the param.
- `dashboard.test.tsx`: the header's title, count and `apiVersion · short names`; no count
  before the `Bookmark`; the cluster-scoped line under a namespace; the Events subtitle.
- `objects-table.test.tsx`, `events-table.test.tsx`: rows filtered by the scope; the Namespace
  column dropped while scoped; the scoped empty state.
- `dashboard-resource-nav/nav.test.tsx`: the picker leads the nav; counts drawn while scoped.
- `kubestore` `reads_test.go`: `KindsIn` counts namespaced kinds and Events in the namespace and
  keeps a cluster-scoped kind's whole count. `cluster` `cacheddata_test.go`: a scoped watch
  counts in the namespace and names it on every frame. `graph`: the argument reaches the service.
- `cluster-cached-data-kinds.test.tsx`: subscribes with the scope; a namespace switch
  re-subscribes; a frame counted in another namespace is dropped.
- `cluster-cached-data-objects.test.tsx`: an `unshared` consumer mounted into a running watch
  gets its snapshot.
