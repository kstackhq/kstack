# Step 2: the namespace scope and the page frame

Add a namespace scope to the window, and the page frame every dashboard page shares: a toolbar
with the scope switcher and the kind switcher, over a header naming the page. The series and its
rules are in [`README.md`](README.md).

Frames, on the _K8s Dashboard (imported)_ page of the Figma file:

- `K8s/ScopeSwitcher`: `Style=Solid` is the namespace switcher, `Style=Ghost` the kind switcher.
- `Kind switcher menu (open)`: opened on a kind, and scrolled to the top.
- The _Page toolbar_ and _Header_ of `Pods (filtered to checkout)`,
  `Events (filtered to checkout)`, `PersistentVolumes (cluster-scoped)` and
  `Dashboard / Namespace overview selected`. The rest of those frames is steps 3 to 5.

No frame draws the namespace switcher's menu; this step settles it.

## What the step settles

**The scope is one namespace or all of them.** It is a `namespace` search param on `_app`,
beside `kubeContext`, `resource` and `chat`, and `retainSearchParams` keeps it across the
switch to chat and back. Absent means all namespaces. Like `resource`, it is validated
leniently: any non-empty string. A new ADR records it, amending
[URL params as window state](../../adr/2026-08-09-url-params-as-window-state.md).

**A cluster switch clears the scope**, as it clears `chat`: the picker's navigation writes
`namespace: undefined`. A namespace belongs to a cluster.

**Only the dashboard reads the scope.** Chat mode ignores it, and no chat send carries it.

**The page frame wraps every dashboard page.** `routes/dashboard.tsx` draws the toolbar, then
the header, then the panel it draws today. The frame replaces today's `<h1>`.

### The toolbar

Left to right: the namespace switcher, a `/`, the kind switcher. The frame's _Ask_ button and
the Events page's _Live_ toggle and _Summarize_ button are not built (_Not built_, below), so the
right side is empty in this step.

**The namespace switcher** (`ScopeSwitcher`, solid) shows a layers icon, _All namespaces_ or
the namespace's name through `VisibleText`, and a chevron. It opens a menu:

- a search box, _Filter namespaces…_, matching a case-insensitive substring of the name;
- _All namespaces_, always first and never filtered out;
- the cluster's namespaces by name;
- the current one marked, and, if the cluster does not list it, still shown at the top with
  _Not found_ beside it;
- the keyboard of the kind switcher's menu (below).

Picking a row writes `namespace` and closes the menu. The list comes from the cache's
`v1/namespaces` objects. Before their `Bookmark` it reads _Loading namespaces…_; with no active
cache, or a cache that does not serve namespaces, it holds _All namespaces_ and the current one
alone.

**The kind switcher** (`ScopeSwitcher`, ghost) shows the page's label through `VisibleText`
and a chevron. A cluster-scoped kind leads with a globe icon. It opens the menu in the frame:

- the search box, _Jump to a resource…_, with the sidebar's match: a case-insensitive prefix
  of a kind's label;
- Overview and Events, then the _Cluster_ and _Namespace_ sections, each group a label over its
  kinds with their counts. The groups are always expanded here; it is a jump list, not the
  sidebar's tree;
- the same kinds as the sidebar: built by `buildDashboardNav` and filtered by `filterNav`, so
  empty kinds hide, the current kind stays, and a search reaches the empty ones;
- the current kind highlighted, and the list opened scrolled to it;
- a footer: _↑↓ navigate_, _↵ open_, _esc close_.

↑ and ↓ move the highlight over rows, skipping labels; Enter opens the highlighted row; Escape
closes. Focus stays in the search box while the arrows move the highlight. Opening a row writes
`resource` and closes the menu.

The two menus share one component, `ScopeMenu`, in
`components/widgets/page-frame/`. It takes rows and labels, and owns the search box, the
keyboard, the scroll-to-current and the footer.

### The header

**A kind's page:** its label as the title, its object count, then its `apiVersion` and short
names in monospace (`v1 · po`). The short names are a static table on the curated kinds in
`GROUPS`. Other kinds draw the `apiVersion` alone, since the catalog carries no short names
(_Data gaps_).

**The count is the rows the page shows.** It is drawn only once the kind's objects have their
`Bookmark`. To keep the two the same, the page reads the objects watch and hands the rows to
`ObjectsTable`, which no longer subscribes itself.

**A cluster-scoped kind under a namespace** reads, under the title, _Cluster-scoped, so the
`<namespace>` filter isn't applied._ The switcher keeps the namespace: it applies again on the
next namespaced page.

**Events:** the title _Events_ and its count, with _Events in `<namespace>`._ under it while a
namespace is chosen.

**Overview:** the title _Overview_. Its body stays _coming soon_ until step 5.

### The scope applies now

A switcher that changed nothing would be a dead control, so this step filters the pages that
exist, client-side:

- `ObjectsTable` of a namespaced kind shows the scope's objects alone, and drops its Namespace
  column while a namespace is chosen.
- `EventsTable` shows the events whose `involvedNamespace` is the scope.
- A filtered table with no rows reads _No `<kind>` in `<namespace>`_.

The watches stay cluster-wide; the sidecar serves no namespaced watch.

**The sidebar follows the scope.** The _Namespace_ section's heading reads _Namespace · all_ or
_Namespace · `<namespace>`_. Its counts are cluster-wide, so while a namespace is chosen its
kind and group rows draw no counts (_Data gaps_: per-namespace counts). Hiding empty kinds still
uses the cluster-wide count, since a kind empty in the cluster is empty in every namespace. The
pinned Events row's count follows the same rule.

### One namespaces watch

urql shares one connection per query and variables, so a consumer that mounts into a running
watch never sees its snapshot (`src/CLAUDE.md`, _Connection lifecycle_). The namespace list and
a Namespaces page would both watch `v1/namespaces`. The namespace switcher's watch is held in a
`ClusterNamespacesProvider` beside `ClusterCachedDataKindsProvider` in `AppLayout`, enabled only
on the dashboard, and subscribes `unshared`. `useClusterCachedDataObjects` gains the
`unshared` option to allow it.

## Not built

- _Ask_. Without the page in a chat's context, it would only repeat the right sidebar's toggle.
- The Events page's _Live_ toggle and _Summarize these events_ (assistant-written).
- The tooltip on the namespace switcher over a cluster-scoped page. The header's line says it.
- More than one namespace in the scope ([`docs/TODO.md`](../../TODO.md#frontend-webview)).
- _Events … plus cluster-wide events that affect it_: it needs to know which cluster-scoped
  objects a namespace depends on.

## Tasks

| #   | Task                                                                                                                                         | Status  |
| --- | -------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| 1   | The ADR; `namespace` on `_app` with `retainSearchParams`; `useNamespaceScope` reading and writing it; the cluster picker clearing it          | Planned |
| 2   | `unshared` on `useClusterCachedDataObjects`; `ClusterNamespacesProvider` in `AppLayout`                                                       | Planned |
| 3   | `ScopeMenu`: search, rows and labels, keyboard, scroll-to-current, footer, light and dark                                                    | Planned |
| 4   | The namespace switcher and the kind switcher over `ScopeMenu`; the short names on `GROUPS`                                                   | Planned |
| 5   | The page frame in `routes/dashboard.tsx`: toolbar and header; the objects watch lifted out of `ObjectsTable`                                 | Planned |
| 6   | The scope applied: `ObjectsTable`, `EventsTable`, the sidebar's heading and counts                                                           | Planned |
| 7   | `src/CLAUDE.md`: _Routing & layout_ (the param), _Dashboard resource nav_ (the heading, counts), the page frame; `docs/TODO.md` for the gaps | Planned |

## Tests

- `_app.test.tsx`: `namespace` validated and kept across a mode switch; absent reads as all.
- The cluster picker: a switch clears `namespace` along with `chat`.
- `scope-menu.test.tsx`: the search filters rows and keeps the always-shown ones; ↑↓ skip
  labels and wrap at neither end; Enter picks; Escape closes; it opens scrolled to the current
  row; rows draw through `VisibleText`.
- The namespace switcher: _All namespaces_ first; namespaces sorted; _Loading namespaces…_
  before the `Bookmark`; a missing current namespace marked _Not found_; a pick writes the param.
- The kind switcher: the sidebar's kinds, sections and counts; the globe on a cluster-scoped
  kind; a pick writes `resource`.
- `dashboard.test.tsx`: the header's title, count and `apiVersion · short names`; no count
  before the `Bookmark`; the cluster-scoped line under a namespace; the Events subtitle.
- `objects-table.test.tsx`, `events-table.test.tsx`: rows filtered by the scope; the Namespace
  column dropped while scoped; the scoped empty state.
- `dashboard-resource-nav/nav.test.tsx`: the heading names the scope; Namespace-section counts
  hidden while scoped; empty kinds still hidden.
- `cluster-cached-data-objects.test.tsx`: an `unshared` consumer mounted into a running watch
  gets its snapshot.
