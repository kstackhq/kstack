# Step 1: the sidebar

Rebuild the dashboard's left sidebar to the mockup. The series and its rules are in
[`README.md`](README.md).

Frames, on the *K8s Dashboard (imported)* page of the Figma file:

- `K8s/Sidebar`: the tree with groups open.
- `Sidebar / Groups collapsed (idle)`: every group closed.
- `Sidebar / Filter applied`: search with a query.
- Local components: `K8s/NavItem`, `K8s/NavGroup`, `K8s/SectionDivider`. `K8s/ProblemBadge` is
  not built in this step (*Not built*, below).

Chat mode's sidebar does not change.

## What the step settles

**The Dashboard/Chat switch stays as it is.** `ModeNav` keeps its order (Chat, then Dashboard),
its look and its place in the card's head. The collapsed pill does not change. The frames' switch
is not drawn.

**The card, top to bottom:** `ModeNav`, the search box, Overview and Events, the *Cluster*
section, the *Namespace* section, then the *Show N empty kinds* toggle.

**Overview and Events are pinned rows.** They are always shown, even while a search matches
neither. Events shows the count of core `v1` Events.

**A kind's section is its scope.** A `Cluster`-scoped kind goes under *Cluster*, a `Namespaced`
kind under *Namespace*. The divider reads *Namespace · all* until step 4 adds a namespace scope.

**A kind's group comes from its API group.** One list orders the groups, and each section draws
only the groups that hold one of its kinds:

| Group | Curated kinds, in order | Also receives |
| --- | --- | --- |
| Workloads | Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs | `apps`, `batch`, `autoscaling`, `policy`; core ReplicationControllers, PodTemplates |
| Infrastructure | Nodes, Namespaces | `node.k8s.io`, `scheduling.k8s.io`, `coordination.k8s.io`, `flowcontrol.apiserver.k8s.io`, `resource.k8s.io`; core ComponentStatuses |
| Network | Services, Ingresses, NetworkPolicies | `networking.k8s.io`, `discovery.k8s.io`, `gateway.networking.k8s.io`; core Endpoints |
| Config | ConfigMaps, Secrets | core ResourceQuotas, LimitRanges |
| Storage | PersistentVolumes, StorageClasses, PersistentVolumeClaims | `storage.k8s.io` |
| Access control | ServiceAccounts, Roles, RoleBindings, ClusterRoles, ClusterRoleBindings | `rbac.authorization.k8s.io`, `certificates.k8s.io` |
| Extensions | CustomResourceDefinitions, APIServices | `apiextensions.k8s.io`, `apiregistration.k8s.io`, `admissionregistration.k8s.io`; any other non-CRD kind |
| Custom resources | — | any other CRD kind |

A discovered kind is listed in its group after the curated kinds, by label. There is no
*Show more…* row. `events.k8s.io` Events are not listed: the pinned Events row is that data.
This replaces the grouping half of
[the dashboard nav merge](../../adr/2026-08-09-dashboard-nav-merge.md). A new ADR records it and
the old one gains `amended_by`. `DashboardResource` stays an open string.

**Group rows toggle; they do not navigate.** A group row shows the sum of its listed kinds'
counts. A deep link to an old group id (`resource=workloads`) reads as a kind this cluster does
not serve.

**Sections and groups collapse, and the state is kept.** Each is a persisted flag
(`usePersistedFlag`, key `dashboard-nav-open:<section>` or `dashboard-nav-open:<section>/<group>`).
With nothing stored, sections are open and groups closed. Navigating to a kind opens its section
and group.

**Kinds with no objects are hidden.** After the `Bookmark`, the tree lists only the kinds the
catalog serves, and hides those with a count of 0. A group with no listed kinds is hidden. The
toggle at the bottom reads *Show N empty kinds* or *Hide empty kinds*, and is a persisted flag
(`dashboard-nav-show-empty`). The active kind is always listed, empty or not.

**Before the `Bookmark`, nothing is hidden.** With no active cache, or while the catalog is
connecting, the tree draws the curated kinds without counts, and the status line under the tree
shows as it does now. No kind is hidden for being empty until the catalog has landed.

**Search filters kinds by name.** A case-insensitive prefix match on a kind's label, over every
kind the catalog serves, empty kinds included. While the query is not empty:

- the tree shows only matching kinds, with their groups and sections open, whatever is stored;
- a section with no match reads *Cluster · no matches* and draws no groups;
- a line under the box reads *N of M kinds*, with *Clear*;
- Escape in the box clears it.

The query is not saved and does not reach the URL.

## Not built

- The filter button beside search.
- Problem badges, the Overview row's badge, and *Show only kinds with problems*: they need a
  health verdict (README, *Data gaps*).
- The *Filter applied* frame's chips, the *Problems only* chip and the *N / M* object counts:
  they need object search.

## Tasks

| # | Task | Status |
| --- | --- | --- |
| 1 | `dashboard-resources.ts`: the pinned rows, the group list and its mapping; `buildDashboardNav` returns sections of groups of kinds, empty kinds marked | Planned |
| 2 | `NavItem`, `NavGroup`, `SectionDivider` to the local components, light and dark | Planned |
| 3 | `DashboardResourceNav`: pinned rows, sections, persisted collapse, the empty-kinds toggle | Planned |
| 4 | Search: the box, the prefix match, the match line, *Clear* and Escape | Planned |
| 5 | The new ADR; `amended_by` on the nav-merge ADR; `src/CLAUDE.md` *Dashboard resource nav*; the pane-state row in `docs/persisted-data.md` | Planned |

## Tests

- `dashboard-resources.test.ts`: each API group and core kind lands in its group and section; a
  CRD in a mapped API group stays there; other CRDs go to Custom resources, other non-CRD kinds to
  Extensions; `events.k8s.io` is dropped; discovered kinds sort after curated ones; group counts
  sum their listed kinds.
- `dashboard-resource-nav.test.tsx`: curated tree without counts before the `Bookmark` and with no
  cache; empty kinds hidden after it, shown by the toggle; the active kind listed while empty;
  collapse persisted and restored; navigating opens the kind's group; a group row does not
  navigate; search matches by prefix, opens groups, reads *no matches* and *N of M kinds*, and
  clears on *Clear* and Escape.
- Cluster strings (CRD labels) drawn through `VisibleText`.
