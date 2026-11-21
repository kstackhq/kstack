---
title: The chrome holds the mode switch, the scope and the global actions
date: 2026-11-21
scope: frontend
status: Accepted
---

# The chrome holds the mode switch, the scope and the global actions

## Context

The main window's shell had grown its global controls in three places. The Chat/Dashboard switch
(`ModeNav`) headed the left sidebar's card, and a collapsed sidebar kept a pill carrying the
switch again. Notifications and Settings sat in a pill at the app bar's right. The namespace scope
was the dashboard nav's first row. The app bar itself was centred on the context omnibox, which
grew with the window.

So switching mode needed the sidebar, or its pill. The scope that decides what the dashboard
counts and shows scrolled with the nav it filtered. And nothing in the bar said which page the
window was on. Issue #1116 brought mockups that move these controls into the chrome.

## Decision

The chrome holds whatever is global to the window; the main card holds the mode's content.

- **A mode rail** (`components/shell/mode-rail/`) runs down the window's left edge, below the app
  bar, at every width and whether or not the sidebar is open. Chat and Dashboard are router links at
  its top; Notifications and Settings sit at its foot.
- **The app bar reads left to right**: a gutter (the window's controls, `HistoryNav`, the left
  sidebar's toggle) as wide as the rail plus the sidebar while the sidebar is open, the cluster chip
  (the omnibox, restyled and left-aligned), the page's title, then on the dashboard the namespace
  chip (`NamespacePicker`) and the right sidebar's toggle. The title is the open chat's, or the
  dashboard's breadcrumbs built from the nav's own tree.
- **One rounded card** holds the left sidebar, the page and the right sidebar, inset from the
  chrome. A collapsed sidebar draws nothing: the rail and the bar's toggle cover what the pill did.
  A kind's page drops its heading, since the breadcrumbs name it.
- The bar is 56px tall, and every mirror of its height moves with it.

The sidebar's width is read once, in `AppLayout`, and handed to both the sidebar and the bar's
gutter, so the divider after the gutter follows a drag live.

This amends [The omnibox is the cluster picker](2026-10-29-the-omnibox-is-the-cluster-picker.md):
the chip is now as wide as its contents and carries padding and gaps, so a press between its
controls lands on the chip and opens nothing. Its popovers start at its left edge at a width of
their own.

## Alternatives considered

- **Keep the switch in the sidebar and add it to the bar only while collapsed.** The switch would
  move with the sidebar's state, and the pill's job would just change hands.
- **A segmented Chat/Dashboard control in the app bar.** The bar already carries the cluster, the
  title and the scope; a third wide control would crowd the title on an ordinary window.
- **Keep the namespace picker in the nav and only mirror it in the breadcrumbs.** The scope would
  still scroll away with the rows it filters, and two controls would edit one parameter.

## Consequences

Mode, scope and settings are one press away whatever the sidebar is doing. The bar's text (the
title and breadcrumbs) sits on the bar's compositor layer, which draws it grayscale-antialiased on
Linux; that layer is what stops the transparent window flashing on resize.

The gutter's width is arithmetic over the layout's numbers (`PAGE_INSET` in `app-bar/bar.tsx`:
the rail, its gap and the card's border). A change to the rail's width or the card's inset changes
that constant too. The right sidebar's drag offsets by the card's inset the same way.

The breadcrumbs read the same tree the nav draws (`useDashboardNav`), so a kind's group in the
bar and in the nav cannot disagree.

## Revisit when

The rail gains a third mode or more global actions than its foot can hold, or the window needs the
left edge back for content.
