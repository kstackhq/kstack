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

-- sandbox_settings: the user's settings for every sandboxed command, in one
-- row. Each <platform>_enabled is whether a sandboxed command may reach that
-- cloud platform as the user.
CREATE TABLE sandbox_settings (
  id             INTEGER PRIMARY KEY CHECK (id = 1),
  aws_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (aws_enabled IN (0, 1)),
  github_enabled INTEGER NOT NULL DEFAULT 1 CHECK (github_enabled IN (0, 1)),
  gcp_enabled    INTEGER NOT NULL DEFAULT 1 CHECK (gcp_enabled IN (0, 1)),
  azure_enabled  INTEGER NOT NULL DEFAULT 1 CHECK (azure_enabled IN (0, 1)),
  updated_at     INTEGER NOT NULL
) STRICT;

INSERT INTO sandbox_settings (id, updated_at) VALUES (1, 0);
