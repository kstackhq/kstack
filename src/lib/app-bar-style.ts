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

// The classes the app bar's controls share with components the bar imports: the
// omnibox and the window's caption buttons. They sit in `src/lib/`, outside
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
