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

// The editors holding work not yet saved, by id. A surface that holds editors
// stays open while any does, so a dismissal never drops a draft or the reason
// its save failed.
import { createContext, useContext, useEffect, useId } from 'react';

export const UnsavedDrafts = createContext<Set<string> | null>(null);

// Adds this editor to the surrounding set while `held`.
export function useHoldsUnsaved(held: boolean) {
  const drafts = useContext(UnsavedDrafts);
  const id = useId();
  useEffect(() => {
    if (!held || !drafts) return;
    drafts.add(id);
    return () => {
      drafts.delete(id);
    };
  }, [drafts, held, id]);
}
