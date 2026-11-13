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

// A section's heading in the dashboard's resource nav: a muted, capitalized label. Lighter
// than the rule above the search box, so the sections read as part of what the search
// filters. The capitals are CSS; the label stays as written.
export function SectionHeading({ label }: { label: string }) {
  return (
    <h3 className="mt-2 truncate p-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase">{label}</h3>
  );
}
