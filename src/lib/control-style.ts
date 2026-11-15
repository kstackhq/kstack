// Copyright 2026 The Kstack Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The looks controls share across areas that do not import each other.

// The app bar's controls share the `APP_BAR_*` classes with components the bar
// imports: the omnibox and the window's caption buttons. They sit outside
// `app-bar/`, so neither has to import the folder that imports it.

// `ghost` is the library variant with no resting fill, so the bar's tint shows
// through and only hover paints. That drops the border with it, hence
// `border-sidebar-border`: the button's base already carries `border` at
// transparent, so a control that is not a `Button` needs the width as well as
// the color.
export const APP_BAR_SHAPE_CLASS = 'rounded-full border-sidebar-border';

// Every bar control is 32px tall: the omnibox, the groups and the buttons in them.
export const APP_BAR_HEIGHT_CLASS = 'h-8';

/** For a control that cannot be a `Button` — the omnibox's wrapper. */
export const APP_BAR_CONTROL_CLASS = `${APP_BAR_SHAPE_CLASS} ${APP_BAR_HEIGHT_CLASS}`;

// Hover is a wash over the bar's own tint rather than the library's fixed
// `bg-muted`. The `dark:` form is needed too: `ghost` carries its own
// `dark:hover:bg-muted/50`, which tailwind-merge only replaces with a class
// in the same variant chain.
export const APP_BAR_HOVER_CLASS = 'hover:bg-app-bar-hover dark:hover:bg-app-bar-hover';

// The look of the controls at the top of the left sidebar: New chat, the namespace
// picker and both search boxes. The fill and the border are the chat composer's, so
// they stay opaque on the sidebar's tint in both themes.
//
// The `dark:` border is needed too: the outline button carries `dark:border-input`,
// which tailwind-merge only replaces with a class in the same variant chain.
export const SIDEBAR_CONTROL_CLASS = 'rounded-lg border-border bg-background shadow-xs dark:border-border dark:bg-card';

// The stroke of a 12px Lucide icon in an `icon-xs` button. Lucide measures it in
// its 24-unit viewBox, so 2.2 draws the design's 1.1px line.
export const SMALL_ICON_STROKE = 2.2;
