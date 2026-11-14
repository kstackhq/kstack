# Figma

How the webview's design in Figma maps to the code in `src/`, and how agents sync the two through the Figma MCP. Two files hold it:

- **[kubetail-ui](https://www.figma.com/design/KoHFVSSKfENrqPidIkJfpO/kubetail-ui)** is the team library: a port of `@kubetail/ui`. Its Cover names the commit it was synced from. An edit there reaches other files only once the library is published.
- **[Desktop](https://www.figma.com/design/ka48YRKD3axbxqJbqR8O2O/Desktop)** is the app. Its pages are Cover, Foundations, Components, Screens and Archive.

## Layout

Every group on a page is a Figma section, laid out so that no two overlap. A new component or screen goes in its group's section, and the section grows to fit it.

- **Components** has one section per folder of `src/components/`, left to right: Icons, Primitives, Shell, Dashboard, Cluster, Chat, Logs. Each section names its folder under its title and stacks its components top to bottom. A popup gets a nested section of its own (`ClusterStatus`, `ClusterPicker`, `MemoryPopover` in Cluster; `ModelPicker` in Chat).
- **Screens** has one section per screen, top to bottom: Platforms, Popups on Shell, Dashboard / Pods, Chat / New, Chat / Open, Chat / Model picker open, Log window. A screen's section holds its *Kstack Light* frame, then its *Kstack Dark* frame.
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
- **Each component links to its source file on `main`**, and its description ends with the commit the design last matched:

  ```
  Synced with src/components/dashboard/status-pill.tsx @ d98a3965
  ```

  The SHA is the last commit that changed that file when the component was synced. An icon names its `lucide-react` version instead. `git log <sha>..main -- <path>` lists the commits since; any output means the design may be behind. Re-syncing a component updates its line.
- **A component edited in Figma ahead of the code says so** in two more lines at the end of its description:

  ```
  Status: dirty
  Change: ModelPicker's trigger draws no chevron.
  ```

  `Status` is `new` (no code draws it yet, so it has no sync line) or `dirty` (it changed since its sync line's commit). No `Status` line means the design matches the code at that commit. Each `Change` line is one change, in plain words; changes made before the code catches up add lines. A change drawn only on a screen, such as where a popup sits, goes on the component it concerns, since a screen has no description.
- **Write a description through `descriptionMarkdown`.** Writing `description` stores `'` and `<` as HTML entities. Put JSX in backticks, or Markdown reads it as HTML.
- **Screens hold instances.** An edit to a component reaches every screen; a screen keeps only its overrides (text, a variant, a color).

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

**An agent never decides which side wins; it asks the user.** The sync line shows when the code moved (`git log <sha>..main -- <path>`), and a `Status` line shows when a Figma component was edited, but only if whoever edited it wrote one. So before overwriting a Figma component from code, or code from a design, the agent names the components it will change and the direction, and waits for an answer.

### Designing ahead of the code

An agent that edits a component's design, rather than syncing it from code, adds a `Change` line for each change and sets `Status: dirty`, or `Status: new` on a component the code does not have. To list what is waiting on code, read every component's description on the Components page and keep those with a `Status` line.

### Code to Figma

1. A person saves a named version of each file the agent will edit (**File → Save to version history**). The plugin API cannot, and agent edits land in the file directly.
2. Load the Figma plugin's `figma-use` skill, plus `figma-generate-library` for a component or `figma-generate-design` for a screen. Call `create_session` with the file's key.
3. Find the component by its code name. Edit the component, not the screens that use it.
4. Use library instances, variables and text styles; follow [Translucent colors](#translucent-colors). Set both collections' modes on anything new.
5. Set the component's link and its sync line to the commit that changed the file.
6. Take one screenshot of the result and check it.
7. A change to kubetail-ui needs a person to publish the library before Desktop sees it.

### Figma to code

1. Load the `figma-design-to-code` skill, then call `get_design_context` on the node.
2. Map library instances to `@kubetail/ui` imports, except the text boxes above, and colors back by [Translucent colors](#translucent-colors).
3. Follow `src/CLAUDE.md`. Run `make test-changed` and `pnpm lint:tailwind`, then open a pull request.
4. Once it merges, set each changed component's sync line to the merged commit and delete its `Status` and `Change` lines. The SHA does not exist before then.

## Security

- **These components draw what the security model rests on** ([`src/CLAUDE.md`](../src/CLAUDE.md#security-invariants)): `Chat/ApprovalRequest` (what it shows, its buttons and their order, how Approve arms), the model's description (one line, italic, quoted, never brighter than the command), and every string drawn through `VisibleText`. A design change to them is a security change: it is recorded under [`docs/security/`](security/), not synced as a restyle.
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
| Log window | Light and dark: a tail with a grep, a gap notice and the landed row |

`ClusterStatus`, `ClusterPicker` and `MemoryPopover` draw every state on the Components page; `ModelPicker/Menu` draws Anthropic's. Not drawn yet: the other popovers (sandbox, scope menu and column picker), the memory row's actions menu and its delete confirm; hover and focus states; the dialog and drawer scrims; the Events page, the object panel and the settings dialog; loading, empty, deleted and error states; Markdown beyond paragraphs and inline code; and the log viewer's selection.

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

Code Connect needs Figma's Organization or Enterprise plan; the team is on Professional. The source links and sync lines stand in for it.
