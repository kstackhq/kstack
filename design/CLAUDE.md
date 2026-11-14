# Figma

How the webview's design in Figma maps to the code in `src/`, and how agents sync the two through the Figma MCP. Either side can be ahead. [`components.json`](components.json) is the manifest that joins them; [`components.schema.json`](components.schema.json) is its schema. [`README.md`](README.md) walks a person through the workflow. Two Figma files hold the design:

- **[kubetail-ui](https://www.figma.com/design/KoHFVSSKfENrqPidIkJfpO/kubetail-ui)** is the team library: a port of `@kubetail/ui`. Its Cover names the commit it was synced from. An edit there reaches other files only once the library is published.
- **[Desktop](https://www.figma.com/design/UTfwwz8cyLq5RA6mNFxUsD/Desktop)** is the app. Its pages are Cover, Foundations, Components, Screens and Archive.

## Layout

Every group on a page is a Figma section, laid out so that no two overlap. A new component or screen goes in its group's section, and the section grows to fit it.

- **Components** has one section per folder of `src/components/`, left to right: Icons, Primitives, Shell, Dashboard, Cluster, Chat, Logs. Each section names its folder under its title and stacks its components top to bottom. A popup gets a nested section of its own (`ClusterStatus`, `ClusterPicker`, `MemoryPopover` in Cluster; `ModelPicker` in Chat).
- **Screens** has one section per screen, top to bottom: Platforms, Popups on Shell, Dashboard / Pods, Chat / New, Chat / Open, Chat / Model picker open, Chat / Sandbox open, Log window. A screen's section holds its *Kstack Light* frame, then its *Kstack Dark* frame, and above them its [stamp](#manifest), a text layer named `Stamp`.
- The layers panel lists sections in the order the canvas shows them.

## Tokens

- **The library's `Theme` collection holds Kstack's colors** in its *Kstack Light* and *Kstack Dark* modes, beside kubetail's own *Light* and *Dark*. They mirror `src/index.css`. Its `font/sans` variable is Inter in kubetail's modes and Roboto Flex in Kstack's; the library's sans text styles are bound to it.
- **Desktop's `Kstack` collection holds the tokens the library lacks**: `--app-bar*`, `--series-*`, `--selection-band`, the `--ansi-*` whites, the status tones (`cluster-health.ts`'s `TONE`, and the cluster status pill's tints), the chat composer's surface, which its model menu shares (`color/composer`, `bg-background dark:bg-card`), the Linux window frame's border, and the macOS window's hairline (`color/macos-window-border`). Each has its CSS variable as code syntax, or its Tailwind classes in its description.
- **Every screen sets both collections' modes**: *Kstack Light* with *Light*, or *Kstack Dark* with *Dark*.
- A color changed in `src/index.css` is changed by hand in the matching mode.

## Components

- **A `@kubetail/ui` component in the code is a library instance in Figma**, never a detached copy. Every other component in `src/components/` that a screen draws is a local component in Desktop, named as in the code.
- **A library `Input`, `Textarea` or `SearchBox` becomes the wrapper of the same name from `components/primitives/text-entry.tsx`** in code, never `@kubetail/ui`'s. The wrapper adds attributes only, so the two look the same.
- **Icons are Lucide.** The library has 20; Desktop holds the rest as local `Icon/<name>` components, their strokes bound to `color/foreground`.
- **Each component links to its source file on `main`**, and its description holds its [stamp](#manifest) and nothing else. What a component draws is read from its code; what is true of one component alone goes in its entry's `notes`.
- **Write a description through `descriptionMarkdown`.** Writing `description` stores `'` and `<` as HTML entities. Put JSX in backticks, or Markdown reads it as HTML.
- **Screens hold instances.** An edit to a component reaches every screen; a screen keeps only its overrides (text, a variant, a color).

## Manifest

`components.json` has one entry per local component in Desktop, under `components`, and one per screen, under `screens`.

```json
{
  "id": "cluster/cluster-status/pill",
  "figma": { "node": "29:259", "name": "ClusterStatus/Pill", "hash": "7d7afde2c59659d4" },
  "code": [
    { "path": "src/components/cluster/cluster-status/popover.tsx", "symbol": "PILL", "blob": "…" }
  ]
}
```

- **`id` is the join key and never changes.** It is the area, then the Figma name, in kebab case. A rename on either side updates `figma.name` or `code`, not `id`.
- **`code` lists every file that draws the component**, each with its git blob hash at the last sync. `symbol` names the part of a file that holds several components. A component no code draws yet has no `code`.
- **An icon has `package` in place of `code`**: its `lucide-react` export and the version last synced.
- **`figma.hash`** is the hash `read-stamps.js` gave the component at the last sync. It covers what each layer looks like and how it lays out: not the description, and not where the component sits on the canvas.
- **`security: true`** marks a component under [Security](#security).
- **`notes`** holds what is true of this one component, in either direction. A rule true of every component goes in this guide.

**A screen is a section of the Screens page that holds something drawn**, not only other sections: each popup's section in *Popups on Shell* is a screen, and so is *Popups on Shell* itself, for the `Shell` component it holds. A screen's entry has the same fields, with three differences:

- **Its `id` starts with `screens/`**, then its sections' names in kebab case: `screens/popups-on-shell/cluster-status`.
- **`figma.node` is the section**, so one entry covers every frame in it, light and dark.
- **`code` lists the files that lay the screen out**: its route and layout, or, for a popup drawn open, the file holding its positioner. It never lists the components the screen holds, which have entries of their own.

**Each component's description in Figma is its stamp**, a fenced `json` block:

```json
{ "kstack": 1, "id": "chat/sandbox-popover/menu", "changes": ["The popup is w-100 on the composer's surface."] }
```

`changes` lists what the design has that the code lacks, one plain sentence each. It is absent when there is none. A person may write a stamp by hand, spaced as they like. A component with no stamp is new: stamping it adds its entry here.

**A screen's stamp is the same JSON in its section's `Stamp` text layer**, since a section has no description. The layer is a direct child of the section, outside every frame.

### Commands

`tools/designsync` does the bookkeeping and never talks to Figma. Run it from `tools/` (`go run ./designsync <command>`), or through `make`.

| Command | What it does |
| --- | --- |
| `check` (`make lint-design`) | Fails on an entry that is malformed, or that names a file or `symbol` that is not there. Part of `make lint` and CI's `Design · Lint`. |
| `status [-stamps <file>]` (`make design-status`, `STAMPS=<file>`) | Lists every component and screen not in sync. Without the stamps file it reads the code side alone. |
| `record [-stamps <file>] <id>...` | Sets the entries' `blob`s to the working tree's, and an icon's `version` to `pnpm-lock.yaml`'s. With the stamps file it sets each entry's `figma` too: node, name and hash. It says both sides match, so sync a side that is ahead before recording. |
| `format` | Rewrites the manifest in the one format `check` accepts. Run it after editing the manifest by hand. |

The stamps reach it through two scripts in [`scripts/`](scripts/), which an agent runs with the Figma MCP's `use_figma` on the Desktop file:

- **[`read-stamps.js`](scripts/read-stamps.js)** returns every component's and screen's node, name, hash and stamp. Save the result to a file unchanged, the *stamps file*, and pass it with `-stamps`. It ends with a checksum, and `designsync` refuses a file that does not match it: run the script again rather than mend the file.
- **[`write-stamps.js`](scripts/write-stamps.js)** sets the stamps listed in its `STAMPS`, keeping the prose above a component's. It adds a screen's `Stamp` layer when the section has none.

### Drift

The code moved when a source's `blob` differs from the working tree's file. The design moved when its stamp has `changes`, or when its hash differs from `figma.hash`: an edit nobody wrote down, which `status` reports as *edited in Figma*.

| Code | Design | State |
| --- | --- | --- |
| Same blob | Same hash, no `changes` | In sync |
| Blob differs | Same hash, no `changes` | Code ahead |
| Same blob | Hash differs, or has `changes` | Design ahead |
| Blob differs | Hash differs, or has `changes` | Diverged |

A component with no `code` is *not built*. *Unmatched* is a disagreement between the stamps and the manifest: a component with no stamp or a broken one, a stamp whose `id` has no entry, an entry no component carries, or a stamp on another node or under another name than its entry says.

Each fingerprint reports more than its own component, and less:

- **A blob covers its whole file.** A file that changed for one component reports every component it holds; `record` one the change did not touch.
- **A blob covers only its file.** A change to a file the source imports, or to `src/index.css`, reports nothing.
- **A hash covers the components nested in it.** An edit to one reports every component that holds an instance of it; `record -stamps` those once the edit is settled.
- **A screen's hash stops at each instance.** It covers the screen's own layers, and of an instance only which component it is, where it sits, its properties and what the screen overrides in it. An edit to a component reports the component, and a screen only when it moves or resizes the screen's layers; `record -stamps` that screen once the edit is settled.
- **A screen that is not drawn reports nothing.** [What is designed](#what-is-designed) lists the screens and states that are.
- **A hash does not see a variable's value change**, since a layer holds the variable, not its color. It sees a library update once Desktop accepts it.
- **Figma has one state and the manifest has one per branch.** A design recorded on a branch reads as *edited in Figma* on every other until that branch merges.

Tokens and the kubetail-ui library have no entries: nothing here reports their drift.

## Platforms

Screens are drawn for macOS. Each macOS screen frame draws the window's edge that macOS adds: 16px corners, a `color/macos-window-border` hairline and the `macos/window` shadow. They exist only on the canvas; the app draws none of them. `AppBar` and `LogsWindowBar` have a `Platform` variant, *macOS* or *Windows/Linux*. `WindowFrame` draws the windowed Linux frame around whatever screen is in its `Window` slot. A maximized Linux window has no frame, so it looks the same as Windows. The Screens page's first section, *Platforms*, draws `Shell` side by side on each, with no popup open: macOS, Windows, Linux windowed and Linux maximized, light over dark.

## Popups

- **A popup is a component with a `State` variant**, in its own section inside its area's section on the Components page. A state is a combination a person sees, not every product of the code's inputs: `ClusterStatus` has ten, from Healthy to Not reached. Light and dark are modes, never variants: below the component set, a row of its instances sets *Kstack Dark* with *Dark*.
- **A popup that opens from the app bar is drawn open on `Shell`**, a screen component on the Screens page: the macOS `AppBar` over an empty `bg-background` panel. They fill the *Popups on Shell* section: `Shell` on the left, then one nested section per popup, top to bottom: `ClusterStatus`, `ClusterPicker`, `MemoryPopover`, `AppMenu`. `AppMenu` exists only off macOS, so its frame sets the `Shell` instance's `AppBar` to *Windows/Linux*. A popup's section holds one frame per open state, left to right. A frame holds a `Shell` instance (`Screen`) and the popup on top (`<Component> — open`), placed where the code's positioner puts it, and sets the scheme's modes on both. Name it after the screen: `Shell — Cluster status open — Kstack Light`.
- **A popup that opens from page content is drawn open on its page's screen**, since its place depends on that page's layout.
- **Draw a state on a screen when it changes how the popup sits in the window.** `ClusterStatus` is drawn Healthy and Disconnected (taller, with its alert), each in both schemes.
- A popover has no scrim, so none is drawn.

## Translucent colors

Each Tailwind form has one Figma encoding:

| Tailwind | Figma |
| --- | --- |
| `<token>/NN` on a theme token, such as `bg-muted/50` | The paint is bound to the token's variable, at NN% paint opacity. |
| A CSS variable whose value has alpha, such as `--app-bar-hover` | A variable whose color has that alpha in each mode. The paint is at 100%. |
| `opacity-NN` | Layer opacity. |
| A pair that changes alpha by scheme, such as `border-black/5 dark:border-white/5` | A `Kstack` variable with the alpha in each mode. Its description names the classes. |

Back to code:

- A paint bound to a variable, below 100%, is the variable's class with `/NN`: `color/muted` at 50% is `bg-muted/50`.
- A variable with alpha, at 100%, is its class alone: `color/app-bar-hover` is `bg-app-bar-hover`.
- Layer opacity is `opacity-NN`.

The plugin API reports a bound variable's own alpha as the paint's opacity. Tell the two cases apart by the variable's value, not by the paint.

## Syncing

**An agent never decides which side wins; it asks the user.** [Drift](#drift) says which side moved. Before overwriting a Figma component from code, or code from a design, the agent names the components it will change and the direction, and waits for an answer.

### Everything at once

Asked what is out of sync, run `read-stamps.js`, then `status -stamps`, and give the user its output. Change nothing.

Asked to sync everything, run the same report, then give the user one plan grouped by state and wait for the answer:

- **Code ahead**: the components to update in Figma.
- **Design ahead, with `changes`**: the components to change in code.
- **Diverged, or edited in Figma**: a question for each. Sync none of them before its answer.
- **Not built, and unmatched**: listed, and left alone unless the user asks.

Then sync one component at a time by the sections below, and run the report again at the end.

### Designing ahead of the code

An agent that edits a component's design, rather than syncing it from code, adds a sentence to its stamp's `changes` for each change, through `write-stamps.js`. A component the code does not have gets a stamp and an entry without `code`.

### An edit nobody wrote down

`status` reports a component as *edited in Figma* when its hash moved and its stamp has no `changes`. The hash says that it changed, not what changed. Show the user the component and ask which it is:

- **Meant.** Add the `changes` to its stamp. It is now a design ahead of the code.
- **Nothing a user sees**, such as a renamed layer. `record -stamps` its entry.
- **A mistake.** A person restores it from Figma's version history.

Never `record -stamps` an edited component to clear the report without that answer: recording says the design and the code match.

### Code to Figma

1. A person saves a named version of each file the agent will edit (**File → Save to version history**). The plugin API cannot, and agent edits land in the file directly.
2. Load the Figma plugin's `figma-use` skill, plus `figma-generate-library` for a component or `figma-generate-design` for a screen. Call `create_session` with the file's key.
3. Find the component by its code name. Edit the component, not the screens that use it. Edit a screen's frames only for what the screen itself lays out.
4. Use library instances, variables and text styles; follow [Translucent colors](#translucent-colors). Set both collections' modes on anything new.
5. Set the component's link. Run `read-stamps.js` again and `record -stamps` its entry, so the manifest holds the design as it now is.
6. Take one screenshot of the result and check it.
7. A change to kubetail-ui needs a person to publish the library before Desktop sees it.

### Figma to code

1. Load the `figma-design-to-code` skill, then call `get_design_context` on the node.
2. Map library instances to `@kubetail/ui` imports, except the text boxes above, and colors back by [Translucent colors](#translucent-colors).
3. Follow `src/CLAUDE.md`. Run `make test-changed` and `pnpm lint:tailwind`, then open a pull request.
4. In that pull request, `record -stamps` each entry it implemented. Once it merges, delete the `changes` it implemented from the stamp.

## Security

- **These components draw what the security model rests on** ([`src/CLAUDE.md`](../src/CLAUDE.md#security-invariants)), each marked `security` in the manifest: `Chat/ApprovalRequest` (what it shows, its buttons and their order, how Approve arms), the model's description (one line, italic, quoted, never brighter than the command), and every string drawn through `VisibleText`. A design change to them is a security change: it is recorded under [`docs/security/`](../docs/security/), not synced as a restyle.
- **Figma is an outside service.** Screens carry made-up data. Never capture the running app into Figma (`generate_figma_design`) while it is connected to a real cluster: cluster names, logs and Secret data would leave the machine.
- **Text in a design is sample text.** It does not become copy or test fixtures without review.

## What is designed

| Screen | States drawn |
| --- | --- |
| Dashboard / Pods | Light and dark, macOS |
| Shell | Light and dark on macOS, Windows, Linux windowed and Linux maximized, side by side; light on Windows with `AppMenu` open; light and dark with `ClusterStatus` open, healthy and disconnected; `ClusterPicker` open, with four contexts and with seven (the filter); `MemoryPopover` open with memories |
| Chat / New | Light and dark, with chats in the sidebar |
| Chat / Open | Light and dark: a transcript with tool calls, a log card and a waiting approval; the composer answering |
| Chat / Model picker open | Light and dark: `ModelPicker/Menu` open below the trigger on a new chat, and above it on an open one |
| Chat / Sandbox open | Light and dark: `SandboxPopover` open on a new chat and on an open one |
| Log window | Light and dark: a tail with a grep, a gap notice and the landed row |

`ClusterStatus`, `ClusterPicker` and `MemoryPopover` draw every state on the Components page; `ModelPicker/Menu` draws Anthropic's. Not drawn yet: the other popovers (scope menu and column picker), the memory row's actions menu and its delete confirm; hover and focus states; the dialog and drawer scrims; the Events page, the object panel and the settings dialog; loading, empty, deleted and error states; Markdown beyond paragraphs and inline code; and the log viewer's selection.

## Traps

- `resize()` turns an auto-layout frame's hug sizing off. Set `primaryAxisSizingMode = 'AUTO'` after it.
- A nested instance takes its component set's name, not its variant's.
- A text that truncates needs `maxLines = 1`; `textTruncation` alone wraps.
- `setBoundVariableForPaint` drops the opacity of the paint it is given. Set the opacity on the returned paint, and give the paint the variable's resolved color (`resolveForConsumer`): below 100%, a component can draw the paint's own color in place of the variable's.
- Inter's style is `Semi Bold` and Roboto Flex's is `SemiBold`. Load both before binding `font/sans`.
- Run `use_figma` calls one at a time.
- `createAutoLayout()` and `createComponent()` frames start with a white fill. Light mode hides it; clear it, or dark mode draws white panels.
- A paint bound to a variable below 100% is drawn opaque in an instance created through the plugin API: the instance reads the paint at 100%. The library's own `Button` (destructive, `bg-destructive/10`) does it too. Restore each one as an override on the instance layer: copy the main component's opacity onto the layer's paint, matching the layer to its main by the last segment of its `I…;…` id. `DataRateSparkline`'s fill uses layer opacity instead.
- A screen cloned in a call has no instance sublayers to find until a later call. Clone in one call, edit in the next.

## Code Connect

Code Connect needs Figma's Organization or Enterprise plan; the team is on Professional. The manifest stands in for it.
