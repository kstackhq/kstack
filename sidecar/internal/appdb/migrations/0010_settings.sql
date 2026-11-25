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

-- settings: the user's single-value settings, a row per setting and scope the
-- user has set. A setting with no row, or with a NULL value, has its default.
-- cluster_id NULL is the setting for every cluster; set, it is that cluster's
-- alone and goes with the cluster.
CREATE TABLE settings (
  key        TEXT    NOT NULL CHECK (key <> ''),
  cluster_id TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  value      ANY     CHECK (typeof(value) IN ('integer', 'text', 'null')),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX settings_scope_key ON settings (ifnull(cluster_id, ''), key);

-- Every mode keeps its row, and every tool setting for every cluster, the only
-- scope ever written. A switch keeps its choice when it is off, and the PATH
-- when it is not the default: the old row cannot tell a default from a choice.
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'tools.' || tool_name || '.enabled', enabled, updated_at, updated_at
  FROM tool_settings WHERE cluster_id IS NULL;
INSERT INTO settings (key, cluster_id, value, created_at, updated_at)
  SELECT 'permissions.approval_mode', cluster_id, mode, updated_at, updated_at
  FROM approval_modes;
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.path', path, updated_at, updated_at
  FROM sandbox_settings WHERE path <> '$PATH';
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.aws.enabled', 0, updated_at, updated_at
  FROM sandbox_settings WHERE aws_enabled = 0;
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.github.enabled', 0, updated_at, updated_at
  FROM sandbox_settings WHERE github_enabled = 0;
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.gcp.enabled', 0, updated_at, updated_at
  FROM sandbox_settings WHERE gcp_enabled = 0;
INSERT INTO settings (key, value, created_at, updated_at)
  SELECT 'sandbox.azure.enabled', 0, updated_at, updated_at
  FROM sandbox_settings WHERE azure_enabled = 0;

DROP TABLE tool_settings;
DROP TABLE approval_modes;
DROP TABLE sandbox_settings;
