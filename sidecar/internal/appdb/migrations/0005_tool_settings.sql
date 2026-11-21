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

-- tool_settings: the user's overrides of a tool's default, which is on. tool_name is
-- the tool's name. cluster_id NULL is the setting for every cluster; set, it is
-- that cluster's alone and goes with the cluster. A row naming a tool this build
-- or machine lacks is kept and ignored.
CREATE TABLE tool_settings (
  tool_name  TEXT    NOT NULL,
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
  updated_at INTEGER NOT NULL
) STRICT;

-- One setting per tool per scope.
CREATE UNIQUE INDEX tool_settings_tool_name ON tool_settings (ifnull(cluster_id, ''), tool_name);
