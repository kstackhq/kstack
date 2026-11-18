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

// A settings pane's group: a heading and its one-line lede over the caller's content.
import { type ReactNode, useId } from 'react';

export function Group({ title, lede, children }: { title: string; lede: string; children: ReactNode }) {
  const headingID = useId();
  return (
    <div role="group" aria-labelledby={headingID} className="flex flex-col gap-2">
      <div className="flex flex-col gap-0.5">
        <h4 id={headingID} className="text-sm font-medium text-popover-foreground">
          {title}
        </h4>
        <p className="text-xs text-muted-foreground">{lede}</p>
      </div>
      {children}
    </div>
  );
}
