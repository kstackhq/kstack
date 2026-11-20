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

-- env_vars: the variables the user gives their commands, NAME=value. cluster_id
-- NULL is a variable for every cluster; set, it is that cluster's commands' alone
-- and goes with the cluster. target is where it reaches: commands in the sandbox,
-- commands outside it, or both. A value is not a secret: any command can print it.
CREATE TABLE env_vars (
  id         TEXT    PRIMARY KEY,
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  name       TEXT    NOT NULL,
  value      TEXT    NOT NULL,
  target     TEXT    NOT NULL CHECK (target IN ('sandbox', 'outside', 'both')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

-- One name per scope.
CREATE UNIQUE INDEX env_vars_name ON env_vars (ifnull(cluster_id, ''), name);

-- env is the user's variables a gated command outside the sandbox was shown with
-- and runs with, a JSON array of NAME=value written with cwd; '' on every other
-- call.
ALTER TABLE tool_calls ADD COLUMN env TEXT NOT NULL DEFAULT '';
