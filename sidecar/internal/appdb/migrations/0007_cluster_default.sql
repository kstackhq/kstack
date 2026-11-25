-- Copyright 2026 The Kstack Authors
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--     http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- is_default marks the user's default cluster: a new window with no cluster of its
-- own opens on it. The partial unique index allows at most one marked row. A row
-- marked for deletion or disabled is never the default: the write that marks or
-- disables it clears is_default too.
ALTER TABLE clusters ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1));
CREATE UNIQUE INDEX clusters_one_default ON clusters (is_default) WHERE is_default = 1;
