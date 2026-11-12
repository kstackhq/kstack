# The new dashboard

The build order for the new dashboard view ([#881](https://github.com/kstackhq/kstack/issues/881)),
from the mockups on the *K8s Dashboard (imported)* page of the
[Kstack Figma file](https://www.figma.com/design/TeS8ZaScKc01RDfYJ06A8N/Kstack). The rules in
[Working a numbered spec](../README.md#working-a-numbered-spec) apply.

The mockups draw the finished product with data Kstack does not have yet. They leave most
interactions, and every empty, loading and error state, undrawn. So the work goes one step at a
time, and each step settles what its frames leave open before it is built.

**Where the series ends.** When the last step lands:

- the left sidebar is the mockup's resource nav: the Dashboard/Chat switch, search, Overview and
  Events, then a *Cluster* section and a *Namespace* section of grouped kinds with live counts;
- the window has a namespace scope, chosen in a page toolbar beside a kind switcher;
- the kind pages, Pods, Events and PersistentVolumes first, draw the mockup's table pages;
- a pod has a detail page, with Overview and Logs tabs;
- the Overview page draws the tiles the existing data can fill;
- every view matches its frame in light and dark themes.

## What is not in this series

- **The assistant panel** (`K8s/AssistantPanel`). The dashboard's right sidebar stays
  `DashboardChat` as it is.
- **The app bar** (`K8s/AppBar`). It stays as it is: the series does not restyle it or change
  its omnibox. The mockups' breadcrumb, verdict pill, refresh and **+** are not built.
- **Multi-cluster tabs.** The window keeps one cluster. The tab strip in the mockups' app bar is
  not built.
- **The Home / cross-cluster start page** (`Home / Cross-cluster start page`). It assumes tabs and
  data sources Kstack does not have.
- **Sidecar changes.** The series reads the streams the sidecar serves today, per #881. A frame
  that needs more data draws what exists and leaves the rest out; it never shows invented or
  placeholder numbers. Each gap gets an issue of its own (*Data gaps*, below).
- **Cluster writes** drawn in the mockups: *Delete* on selected pods, *Roll back*.
- **The debug terminal** (`Pod detail / Debug terminal`). It is a feature, not a view.
- **Assistant-written text in a page**: the *Summary* card, *Why it is failing*, and the
  suggestion buttons under them.

## The shape of the work

**Steps run one after another.** Each step is one pull request into `wip/dashboard`. The step's
spec, `docs/specs/dashboard/<n>-<slug>.md`, is the first commit of that PR. It names the
step's frames, settles their open questions, and lists the tasks and tests. The spec and the code
are reviewed together.

**Every step ships.** After each merge, `wip/dashboard` is a working app: no half-built page and no
dead link. A step that replaces a view replaces it whole.

**Every step keeps these rules:**

- the delta-watch rules in the root `CLAUDE.md`: no empty state before the `Bookmark`, and the
  `Bookmark` detected by `type`;
- cluster strings drawn through `VisibleText`;
- light and dark themes from the same tokens, checked against the frame by hand;
- tests beside each changed file, and every changed line covered (`coverage / Gate`);
- `src/CLAUDE.md` updated in the same PR when a convention or a component it names changes.

**Shared pieces are built by the first step that needs them**, and later steps reuse them. The
mockups' local components are the list: `NavItem`, `NavGroup`, `SectionDivider` and
`ProblemBadge` (step 1), `ScopeSwitcher` (step 2), `StatusPill` (step 3), `PodHeader`,
`PodMenuItem` (step 4), `AttentionRow`, `StatTile` (step 5). `AssistantCard` is not built: it
holds assistant-written text.

## Steps

| Step | Builds | Frames | Needs |
| --- | --- | --- | --- |
| 1 | The sidebar | `K8s/Sidebar`, `Sidebar / Filter applied`, `Sidebar / Groups collapsed (idle)` | — |
| 2 | The namespace scope and the page frame | `K8s/ScopeSwitcher`, `Kind switcher menu (open)`, each page's toolbar and header | 1 |
| 3 | The kind pages | `Pods`, `Events` and `PersistentVolumes` frames | 2 |
| 4 | The pod detail page | `Pod detail / Overview`, `Pod detail / Logs`, `Pod switcher menu (open)` | 3 |
| 5 | The Overview page | `Overview (all namespaces)`, `Overview (filtered to checkout)` | 2 |

### 1. The sidebar

Rebuild `DashboardResourceNav` and the left sidebar card to the mockup. The Dashboard/Chat switch
(`ModeNav`) moves into the card's head. Under it are search, then Overview and Events, then the
*Cluster* section (Infrastructure, Storage, Access control, Extensions), then the *Namespace*
section (Workloads, Network, Config, Storage). Groups collapse, and each kind row shows its live
count.

Open questions:

- How `DASHBOARD_NAV`'s groups map to the two sections, and where discovered kinds go.
- What search matches. The mockup's chips (`web-*`) and *N / M* counts match object names. Today
  only kind names can be matched without new data.
- What the filter button beside search opens.
- Whether empty kinds hide by default (*Show 6 empty kinds*), and whether a group's collapsed
  state is saved.
- Whether problem badges and *Problems only* wait for a data gap to close (*Data gaps*).

### 2. The namespace scope and the page frame

Add a namespace scope to the window. Then build the page frame every dashboard page shares: the
toolbar (`ScopeSwitcher`, then a kind switcher in the breadcrumb, then the page's actions on the
right, *Ask* among them) and the header (title, count, kind's short names).

The kind switcher is a menu of the sidebar's tree with *Jump to a resource…* and keyboard
navigation. It opens scrolled to the current kind.

Open questions:

- Whether the namespace is a search param on `_app`, kept across modes like `kubeContext` and
  `resource`. This is a decision for an ADR, written in this step.
- Whether the scope holds one namespace or several.
- How the sidebar's *Namespace · all* section header and the scope switcher share the scope.
- What *Ask* does, with no assistant panel.
- Whether the scope filters today's tables, so the switcher is not a dead control.

### 3. The kind pages

Rebuild `ObjectsTable` and `EventsTable` into the mockup's table pages: status filter tabs with
counts (*All · Running · Failing · Unknown*), a text filter, a label chip, a column picker, row
selection with a selection bar, per-row actions, and a footer. Pods, Events and
PersistentVolumes are drawn. Every other kind takes the same page with its own columns.

Open questions:

- Which selection-bar actions are built. *View logs together* and *Ask about selected* read;
  *Delete* writes and is out of the series.
- What the text filter matches (*name, label or node*), and whether it is saved.
- Which per-row actions are built, since the debug terminal is out.

### 4. The pod detail page

Add a route for one object, with a pod's page first. A pod's header shows its name, status,
owner, node, IP, age and restarts. Its tabs are Overview (containers, conditions, related
objects, labels), Logs (the log viewer), Events and YAML. The pod switcher in the breadcrumb lists
the kind's other pods with their status.

Open questions:

- The route's shape, and how an object is named in it. Per the root `CLAUDE.md`, the webview never
  builds or parses an `ObjectID`.
- How the *Related* card finds an object's relatives from the cached data.
- Whether the Logs tab is the dashboard's own viewer or the log window's.
- What other kinds get before they have a detail page of their own.

### 5. The Overview page

Build the Overview page from the tiles the existing data can fill: the title block, *Needs
attention* drawn from the cluster's objects and events, the stat tiles that need only counts
(*Nodes ready*, *Pods running*), and *Namespaces* with workload counts. Every tile that needs a
data gap is left out until its issue lands.

Open questions:

- What ranks *Needs attention*, and what counts as an item, without external sources.
- Whether *Recent changes* can be drawn from Kubernetes Events alone.

## Data gaps

Each needs its own issue, outside this series. A step that meets one draws the view without it.

- **Per-namespace counts.** Per-kind counts are cluster-wide. The sidebar's *N / M* counts and a
  scoped Overview need them per namespace.
- **Short names.** The catalog carries none, so a kind's header names only the built-ins'.
- **Object search across kinds.** Object names, for the sidebar's search.
- **A health verdict per object and per kind.** Problem badges, *Problems only*, the status tabs'
  counts and *Needs attention* need one shared definition of a problem.
- **Usage metrics.** The CPU and Memory columns, and the Namespaces table's bars.
- **Resource requests across the cluster.** The *CPU requested* and *Memory requested* tiles.
- **Cluster facts.** Kubernetes version, provider, region, and an available upgrade.
- **External sources.** Prometheus alerts and Argo CD syncs in *Needs attention* and *Recent
  changes*.
- **Assistant-written summaries.** The *Summary* card and *Why it is failing*.

## When it lands

- `src/CLAUDE.md`: *Routing & layout* (the namespace scope, the detail route), the window
  layout's sidebars, the context omnibox, and *Dashboard resource nav*.
- The namespace scope's ADR, from step 2. If the nav's grouping changes, a new ADR supersedes
  [the dashboard nav merge](../../adr/2026-08-09-dashboard-nav-merge.md), and its links are
  repointed.
- `docs/TODO.md`: the data gaps not yet filed as issues.
- Delete `docs/specs/dashboard/`, and close #881.

## Verification

Each step: `make check-changed`, a green `coverage / Gate`, then the app run against a cluster.
Each of the step's frames is compared in light and dark themes, along with the states the frames
do not draw: no cluster, connecting, before the `Bookmark`, and empty.
