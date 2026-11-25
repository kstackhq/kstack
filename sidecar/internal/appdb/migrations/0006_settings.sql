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

-- permission_rules: the user's rules over the actions sandboxed commands
-- send, each a permissions.Rule. chat_id NULL is an always rule; set, the
-- rule lasts for that chat and goes with it. No effect, level, principal or
-- target is enumerated here: settings.CheckRule refuses on write what Go
-- cannot apply, and a read turns a row it cannot read into a refusal.
CREATE TABLE permission_rules (
  id           TEXT    PRIMARY KEY CHECK (id <> ''),
  chat_id      TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  effect       TEXT    NOT NULL,
  access_level TEXT    NOT NULL,
  principal    TEXT    NOT NULL,
  target       TEXT    NOT NULL,
  scope        TEXT    NOT NULL,
  service      TEXT    NOT NULL,
  operation    TEXT    NOT NULL,
  resource     TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
) STRICT;

-- A chat's rules, or the always ones, in the order they are read.
CREATE INDEX permission_rules_chat_idx ON permission_rules (chat_id, created_at, id);

-- permission_modes: an account's mode, by target. account is the account a
-- scope's first segment names, or '*' for the target's default. A cloud
-- account has no record, so its row stays until the user removes it.
CREATE TABLE permission_modes (
  target     TEXT    NOT NULL,
  account    TEXT    NOT NULL,
  mode       TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (target, account)
) STRICT;

-- A cluster's mode and rules go with it: the rules whose scope is the
-- cluster's id or starts with it and a '/'. substr, never LIKE, which reads
-- '_' and '%' in an id as wildcards.
CREATE TRIGGER clusters_permissions_delete AFTER DELETE ON clusters
BEGIN
  DELETE FROM permission_modes WHERE target = 'kubernetes' AND account = OLD.id;
  DELETE FROM permission_rules WHERE target = 'kubernetes'
    AND (scope = OLD.id OR substr(scope, 1, length(OLD.id) + 1) = OLD.id || '/');
END;

-- folder_grants: a folder the sandbox opens to a run. chat_id NULL is an always
-- grant; set, the grant lasts for that chat and goes with it.
CREATE TABLE folder_grants (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  folder     TEXT    NOT NULL CHECK (folder <> ''),
  access     TEXT    NOT NULL CHECK (access IN ('read', 'write')),
  created_at INTEGER NOT NULL
) STRICT;

-- One grant per folder, always or in a chat.
CREATE UNIQUE INDEX folder_grants_folder ON folder_grants (ifnull(chat_id, ''), folder);

-- A chat's grants, or the always ones, in the order they are read.
CREATE INDEX folder_grants_chat_idx ON folder_grants (chat_id, created_at, id);

-- executables: the programs the user asked the probe to check, in the order
-- added. A row is read ORDER BY created_at, rowid, since two can be added in
-- the same millisecond.
CREATE TABLE executables (
  name       TEXT    PRIMARY KEY,
  invocation TEXT    NOT NULL,
  created_at INTEGER NOT NULL
) STRICT;

-- shell_path: the login shell's PATH as the last read that answered gave it,
-- joined by ':', for a launch whose own read fails. Written by Kstack alone.
-- No row before a first read answers.
CREATE TABLE shell_path (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  path TEXT    NOT NULL
) STRICT;

-- chat_grants' and tool_settings' rows are not copied, by decision: no install
-- is owed them.
DROP TABLE chat_grants;
DROP TABLE tool_settings;
