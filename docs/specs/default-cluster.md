---
title: The default cluster
scope: sidecar, webview
status: Proposed
---

# The default cluster

**Needs:** nothing beyond `main`. **Answers:** [#467](https://github.com/kstackhq/kstack/issues/467).

Go paths below are under `sidecar/internal/` unless they say otherwise.

## In short

Today a window with no `kubeContext` param opens on the kubeconfig's current context. When that
context is gone, the window resolves to no cluster and the omnibox draws an empty name, even
though other clusters are listed. When it is present but unreachable, the window opens on a
cluster that never connects.

After this change, the user can pick a **default cluster** in Settings > Clusters. A new window
opens on the default cluster first, the kubeconfig's current context second. With neither
usable, it opens on no cluster and says so. Once a window has a cluster, only the user moves it,
unless that cluster stops being usable.

## What is not in this change

- **No change to chats scoped to a missing cluster.** The existing notice stays as it is.
- **No automatic fallback** from an unreachable cluster, and no new UI for one.
- **No write to the kubeconfig.** The default cluster is Kstack's alone.
- **No cloud clusters.** Only a kubeconfig cluster can be the default.

## Design

### 1. Names

- **The kubeconfig's current context** is the context its `current-context` names. It keeps
  every name it has today: `status.source.kubeconfig.isDefault` on the wire and
  `KubeConfig.currentContext` in the webview.
- **The default cluster** is the cluster the user picked in Settings. It is served as
  `spec.isDefault`.
- **The default context** is what a new window opens on: the default cluster's context if it is
  usable, else the current context. The webview serves it as `KubeConfig.defaultContext` (§4).

### 2. Storage

Migration `0007_cluster_default.sql` adds a column to `clusters`. Its header comment explains the
column and the index, as `0001_init.sql`'s does for the others:

```sql
-- is_default marks the user's default cluster: a new window with no cluster of its
-- own opens on it. The partial unique index allows at most one marked row. A row
-- marked for deletion or disabled is never the default: the write that marks or
-- disables it clears is_default too.
ALTER TABLE clusters ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1));
CREATE UNIQUE INDEX clusters_one_default ON clusters (is_default) WHERE is_default = 1;
```

The rules, in `services/cluster`:

- **Setting** the default clears the old one and marks the new one in one transaction. Both rows
  move `updated_at`, so `clusterCollectionWatch` sends a `Modified` for each.
- **A call that changes nothing writes nothing.** Setting the current default, or clearing a
  cluster that isn't the default, leaves `updated_at` alone, sends no `Modified`, and returns the
  record.
- **Marking a row for deletion clears its default** in the same write (`stmtMarkCluster`). So a
  marked row never holds the mark, and the user can pick a new default while the old one is
  torn down.
- **Disabling a cluster clears its default** in the same write (`stmtSetClusterEnabled`).
  Enabling it again does not bring the default back. This is intended: the user picks again.
- **An archived cluster keeps its mark.** If its context comes back to the kubeconfig, the same
  record returns and is the default again.

`clusterDefaultSet` refuses, with `KSTACK_VALIDATION_ERROR`:

- a row marked for deletion, like the other toggles (`delete_requested_at IS NULL`);
- `isDefault: true` on a disabled cluster;
- `isDefault: true` on a cloud cluster;
- `isDefault: true` on an archived cluster, one its kubeconfig no longer declares.

Clearing is allowed on any unmarked row, archived ones included.

### 3. The wire

```graphql
type ClusterSpec {
  # …
  "Whether the user picked this cluster as the default: a new window opens on it. At most one cluster is the default."
  isDefault: Boolean!
}

type Mutation {
  # …
  "Make a cluster the default, or stop it being one. Setting it clears the previous default, whose change arrives on `clusterCollectionWatch`; a call that changes nothing writes nothing. Refused (KSTACK_VALIDATION_ERROR) for a cluster marked for deletion, and, when setting, for a disabled, archived or cloud cluster. Returns the cluster."
  clusterDefaultSet(id: ClusterID!, isDefault: Boolean!): Cluster!
}
```

`ClusterSpec` in `services/cluster/clusters.go` gains `IsDefault`. `ClusterRuntimeSpec` doesn't
change: no pass acts on the default.

### 4. Which cluster a window opens on

`KubeConfigProvider` (`src/lib/kube-config.tsx`) adds `defaultContext` to `KubeConfig`, among the
enabled clusters present in the kubeconfig:

1. the default cluster's context;
2. else `currentContext`;
3. else `''`.

`useActiveKubeContext` (`src/lib/active-kube-context.tsx`) reads `defaultContext` where it reads
`currentContext` today. It resolves the `kubeContext` param if it names a usable context, else
`defaultContext`.

**A window fixes its cluster in the param.** `useFixWindowContext`, beside `useActiveKubeContext`
and called once by `AppLayout` (`src/layouts/app-layout.tsx`), writes the resolved context into
the `kubeContext` param whenever the two differ, replacing the history entry. It writes nothing
until the clusters snapshot is complete, and nothing while no cluster resolves. From then on the
param holds the window's cluster:

- Changing the default, or the kubeconfig's current context, moves no open window. Only new
  windows follow it. Today a `kubectl config use-context` moves every window without a param;
  after this change it doesn't.
- A window that resolves no cluster writes nothing, so it picks up a fix to the setting or the
  kubeconfig as it happens, and then fixes that cluster.
- A param naming a context that is gone yields to `defaultContext`, as today, and the param is
  rewritten to it. That is the only switch the user doesn't make, and it keeps today's
  behaviour for the namespace scope and the open chat. The window stays put if the old context
  returns.

### 5. What the window shows

| Case | Omnibox | Dashboard | Chat |
| --- | --- | --- | --- |
| A cluster resolves, connecting or connected | Unchanged: its name and its health dot | Unchanged | Unchanged |
| A cluster resolves and is unreachable | Unchanged: its name, and the health dot's failing verdict | Unchanged | Unchanged |
| No cluster resolves, others are listed | "No cluster selected"; a click opens the picker, as today | "No cluster selected", with a line pointing to the omnibox | Unchanged |
| The kubeconfig has no contexts | Unchanged: "No kubeconfig" | Unchanged | Unchanged |

An unreachable cluster stays selected. Kstack offers no other cluster for it and waits for no
timeout. The existing health verdict says what is wrong.

On the dashboard, "No cluster selected" wraps the route: `src/routes/dashboard.tsx` draws it in
place of the kinds, objects and events pages, which today each fall through to "No synced cache
for this cluster yet".

### 6. Settings > Clusters

- The default cluster shows a **Default** badge beside its name, drawn with the panel's
  `Badge`. The badge shows on an archived row too.
- A present, enabled row's menu gains **Set as default**, or **Remove as default** on the
  default's row. A disabled row offers neither.
- An archived default's row keeps its delete button alone. Deleting it clears the default.
- The panel's list order doesn't change.

## Decisions this change asks for

1. **The default cluster is a column, not a `settings` row.** It rides `clusterCollectionWatch`
   with the cluster's other toggles, it goes with its row, and the table enforces one default.
   A `settings` row would need its own watch and a check for a stale cluster id. Recommended.
2. **No automatic fallback from an unreachable cluster.** Kstack can act on a cluster, so it
   never moves a window to one the user did not choose. This removes any question of how long
   to wait. Recommended, and recorded as an ADR when it lands.
3. **A window fixes its cluster in the param once one resolves.** It follows from 2: a default
   changed in Settings would otherwise move every open window, the Settings window included,
   carrying the old cluster's namespace scope and chat along. Recommended, and recorded in the
   same ADR.

## Tasks

| # | Task | Files | Needs | Status |
| --- | --- | --- | --- | --- |
| 1 | Migration `0007`; `ClusterSpec.IsDefault`; `SetDefault` and its refusals; marking and disabling clear the mark | `appdb/migrations/0007_cluster_default.sql`, `services/cluster/{clusters,service,statements}.go`, their tests | — | Planned |
| 2 | `spec.isDefault`, `clusterDefaultSet` | `sidecar/graph/schema.graphqls`, `sidecar/graph/schema.resolvers.go`, generated code, their tests | 1 | Planned |
| 3 | `KubeConfig.defaultContext`; `useActiveKubeContext` reads it; `useFixWindowContext` fixes the window's cluster | `src/lib/kube-config.tsx`, `src/lib/active-kube-context.tsx`, `src/lib/clusters.tsx`, `src/layouts/app-layout.tsx`, their tests | 2 | Planned |
| 4 | The "No cluster selected" states | `src/components/shell/context-omnibox/omnibox.tsx`, `src/routes/dashboard.tsx`, their tests; the Figma components and `design/components.json` | 3 | Planned |
| 5 | The badge and the menu items | `src/components/settings/settings-dialog/clusters.tsx`, `src/lib/cluster-mutations.ts`, their tests; the Figma components and `design/components.json` | 2 | Planned |
| 6 | Docs, per *When it lands* | see there | 1–5 | Planned |

**Order:** 1, then 2, then 3 and 5 in either order, then 4, then 6.

## Tests

**`services/cluster`**

- Setting a default marks that row and clears the old one, in one write.
- Setting the current default, or clearing a cluster that isn't the default, leaves
  `updated_at` alone.
- Clearing the default leaves no row marked.
- Setting a disabled, archived or cloud cluster as the default is refused, and nothing changes.
- Setting or clearing a row marked for deletion is refused.
- Marking the default for deletion clears its mark in the same write.
- Setting a new default while the old one is being torn down succeeds.
- Disabling the default clears its mark, and enabling it again leaves it unmarked.
- An archived default keeps its mark, and keeps it when its context returns.
- Clearing an archived default is allowed.

**`appdb`**

- `TestMigration0007AddsTheClusterDefault`: existing rows read `is_default = 0`, and a second
  marked row is refused.

**Webview**

- `defaultContext`: the default cluster's context wins over `currentContext`; a missing,
  disabled or archived default cluster falls to `currentContext`; neither gives `''`.
- `useActiveKubeContext`: a usable param wins over `defaultContext`; a gone one yields to it.
- `useFixWindowContext` writes the resolved context into the param, replacing the history
  entry, and writes nothing before the snapshot is complete. A later change to `defaultContext`
  moves the window nowhere. A window that resolves nothing writes nothing, then moves to and
  fixes a cluster once one resolves. A gone param is rewritten to `defaultContext`.
- The omnibox and the dashboard draw "No cluster selected" when contexts are listed and none
  resolves; the dashboard draws it in place of each page.
- Settings draws the badge on the default's row alone, including an archived one; the menu
  offers **Set as default** or **Remove as default** and calls `clusterDefaultSet`; a disabled
  row offers neither.

## Security

No boundary moves. The default only picks which cluster a new window opens on, from clusters
the user already enabled. Switching an open window is still a user action. No security record.

## When it lands

- **`docs/persisted-data.md`**: the App DB row lists migration `0007` and its test.
- **`sidecar/CLAUDE.md`** or the cluster service's guide: `spec.isDefault`, the one-default
  index, and that marking and disabling clear the mark.
- **`src/CLAUDE.md`**, *Window state*: replace "falls back to the kubeconfig current context"
  with the resolution in §4, and say that `useFixWindowContext` fixes the window's cluster in the
  param.
- **ADR**: no automatic fallback, and a window fixes its cluster once (*Decisions*, 2 and 3).
- **`design/`**: the badge, the menu items and the "No cluster selected" states synced, per
  `design/CLAUDE.md`.
- Close #467.

## Verification

Run the [verification commands](README.md#verification-commands), including the wire checks.

By hand, `pnpm tauri dev` with two contexts, `a` (the kubeconfig's current context) and `b`:

1. A new window opens on `a`. Set `b` as the default in Settings: the open window stays on `a`,
   a new window opens on `b`, and `b` shows the badge.
2. Stop `b`'s cluster. A new window still opens on `b`, which shows as unreachable.
3. Remove `b` from the kubeconfig. A new window opens on `a`, and `b`'s archived row keeps the
   badge.
4. Point `current-context` at a context that doesn't exist, and remove the default. A new window
   shows "No cluster selected". Set `a` as the default, and that window moves to `a`.
5. Delete `b` from Settings, and before its teardown ends, set `a` as the default. It succeeds.
