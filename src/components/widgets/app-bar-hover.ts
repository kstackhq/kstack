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

// Hover is a wash over the bar's own tint rather than the library's fixed
// `bg-muted`. The `dark:` form is needed too: `ghost` carries its own
// `dark:hover:bg-muted/50`, which tailwind-merge only replaces with a class
// in the same variant chain.
//
// It sits outside `app-bar/` because the window's caption buttons wear it too,
// and the app bar imports those.
export const APP_BAR_HOVER_CLASS = 'hover:bg-app-bar-hover dark:hover:bg-app-bar-hover';
