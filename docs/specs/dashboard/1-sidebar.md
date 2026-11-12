# Step 1: the sidebar

Rebuild the dashboard's left sidebar to the mockup. The series and its rules are in
[`README.md`](README.md).

Frames, on the _K8s Dashboard (imported)_ page of the Figma file:

- `K8s/Sidebar`: the tree with groups open.
- `Sidebar / Groups collapsed (idle)`: every group closed.
- `Sidebar / Filter applied`: search with a query.
- Local components: `K8s/NavItem`, `K8s/NavGroup`, `K8s/SectionDivider`. `K8s/ProblemBadge` is
  not built in this step (_Not built_, below).

Chat mode's sidebar does not change.

## What the step settles

**The Dashboard/Chat switch stays as it is.** `ModeNav` keeps its order (Chat, then Dashboard),
its look and its place in the card's head. The collapsed pill does not change. The frames' switch
is not drawn.

**The card, top to bottom:** `ModeNav`, Overview and Events, the search box, the _Cluster_
section, then the _Namespace_ section. This departs from the
frames, which put search first. A rule sets the pinned rows apart: they are pages, not kinds.
The search box sits above the kinds it filters and stays there as they scroll. The sections
are headed by a muted, capitalized label, not `K8s/SectionDivider`'s ruled divider, so the rule
above the search box is the card's only one and the sections read as within its scope.

**Overview and Events are pinned rows.** They are always shown, even while a search matches
neither. Events shows the count of core `v1` Events.

**A kind's section is its scope.** A `Cluster`-scoped kind goes under _Cluster_, a `Namespaced`
kind under _Namespace_.

**A kind's group comes from its API group.** One list orders the groups, and each section draws
only the groups that hold one of its kinds:

| Group            | Curated kinds, in order                                                  | Also receives                                                                                                                        |
| ---------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| Workloads        | Pods, Deployments, StatefulSets, DaemonSets, ReplicaSets, Jobs, CronJobs | `apps`, `batch`, `autoscaling`, `policy`; core ReplicationControllers, PodTemplates                                                  |
| Infrastructure   | Nodes, Namespaces                                                        | `node.k8s.io`, `scheduling.k8s.io`, `coordination.k8s.io`, `flowcontrol.apiserver.k8s.io`, `resource.k8s.io`; core ComponentStatuses |
| Network          | Services, Ingresses, NetworkPolicies                                     | `networking.k8s.io`, `discovery.k8s.io`, `gateway.networking.k8s.io`; core Endpoints                                                 |
| Config           | ConfigMaps, Secrets                                                      | core ResourceQuotas, LimitRanges                                                                                                     |
| Storage          | PersistentVolumes, StorageClasses, PersistentVolumeClaims                | `storage.k8s.io`                                                                                                                     |
| Access control   | ServiceAccounts, Roles, RoleBindings, ClusterRoles, ClusterRoleBindings  | `rbac.authorization.k8s.io`, `certificates.k8s.io`                                                                                   |
| Extensions       | CustomResourceDefinitions, APIServices                                   | `apiextensions.k8s.io`, `apiregistration.k8s.io`, `admissionregistration.k8s.io`; any other non-CRD kind                             |
| Custom resources | —                                                                        | any other CRD kind                                                                                                                   |

A discovered kind is listed in its group after the curated kinds, by label. There is no
_Show more…_ row. `events.k8s.io` Events are not listed: the pinned Events row is that data.
This replaces the grouping half of
[the dashboard nav merge](../../adr/2026-08-09-dashboard-nav-merge.md). A new ADR records it and
the old one gains `amended_by`. `DashboardResource` stays an open string.

**Group rows toggle; they do not navigate.** A group row shows the sum of its listed kinds'
counts. A closed group holding the current kind draws as selected, so the selection stays in
view. A deep link to an old group id (`resource=workloads`) reads as a kind this cluster does
not serve.

**Groups collapse, and the state is kept.** Each is a persisted flag (`usePersistedFlag`, key
`dashboard-nav-open:<section>/<group>`). With nothing stored, each section's first group is open (Infrastructure, Workloads) and the
rest are closed. Navigating to a
kind opens its group. Sections do not collapse: with every group closed, a section is a few rows.

**Kinds with no objects are hidden.** After the `Bookmark`, the tree lists only the kinds the
catalog serves, and hides those with a count of 0. A group with no listed kinds is hidden. There
is no toggle to show them: a search reaches them, and the active kind is always listed, empty or
not.

**Before the `Bookmark`, nothing is hidden.** With no active cache, or while the catalog is
connecting, the tree draws the curated kinds without counts, and the status line under the tree
shows as it does now. No kind is hidden for being empty until the catalog has landed.

**Search filters kinds by name.** A case-insensitive prefix match on a kind's label, over every
kind the catalog serves, empty kinds included. While the query is not empty:

- the tree shows only matching kinds, with their groups open, whatever is stored;
- a section with no match reads _Cluster · no matches_ and draws no groups;
- a line under the box reads _N of M kinds_, with _Clear_;
- Escape in the box clears it.

The query is not saved and does not reach the URL.

## Not built

- The filter button beside search.
- Problem badges, the Overview row's badge, and _Show only kinds with problems_: they need a
  health verdict (README, _Data gaps_).
- The _Filter applied_ frame's chips, the _Problems only_ chip and the _N / M_ object counts:
  they need object search.

## Tasks

| #   | Task                                                                                                                                     | Status |
| --- | ---------------------------------------------------------------------------------------------------------------------------------------- | ------ |
| 1   | `dashboard-resources.ts`: the pinned rows, the group list and its mapping; `buildDashboardNav` returns sections of groups of kinds       | Done   |
| 2   | `NavItem`, `NavGroup` to the local components, and `SectionHeading`, light and dark                                                      | Done   |
| 3   | `DashboardResourceNav`: pinned rows, sections, persisted collapse, the empty-kinds toggle                                                | Done   |
| 4   | Search: the box, the prefix match, the match line, _Clear_ and Escape                                                                    | Done   |
| 5   | The new ADR; `amended_by` on the nav-merge ADR; `src/CLAUDE.md` _Dashboard resource nav_; the pane-state row in `docs/persisted-data.md` | Done   |

## Tests

- `dashboard-resources.test.ts`: each API group and core kind lands in its group and section; a
  CRD in a mapped API group stays there; other CRDs go to Custom resources, other non-CRD kinds to
  Extensions; `events.k8s.io` is dropped; discovered kinds sort after curated ones; group counts
  sum their listed kinds.
- `dashboard-resource-nav/nav.test.tsx`: curated tree without counts before the `Bookmark` and with no
  cache; empty kinds hidden after it; the active kind listed while empty;
  collapse persisted and restored; navigating opens the kind's group; a group row does not
  navigate; search matches by prefix, opens groups, reads _no matches_ and _N of M kinds_, and
  clears on _Clear_ and Escape.
- Cluster strings (CRD labels) drawn through `VisibleText`.
