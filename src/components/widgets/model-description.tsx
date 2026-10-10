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
// The model's description of a call, drawn one way wherever it sits: italic,
// quoted, the text in a truncate span with the quotes outside it so the closing
// one survives the ellipsis, and no title, since a native tooltip draws it raw.
import { VisibleText } from '@/components/widgets/visible-text';

export function ModelDescription({ line }: { line: string }) {
  return (
    <span className="flex min-w-0 italic">
      <span>“</span>
      <span className="min-w-0 truncate">
        <VisibleText text={line} />
      </span>
      <span>”</span>
    </span>
  );
}
