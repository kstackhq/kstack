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

-- app.db is the durable, app-level SQLite database (one schema_migrations
-- sequence, owned by internal/appdb). Timestamps are unix millis.
--
-- Nothing has shipped, so a form change edits this file rather than adding a
-- second migration — docs/adr/2026-08-29-schema-edit-not-migration.md.

-- clusters: every cluster the app knows. This table owns a cluster's identity and
-- the user's choices; the beehive store holds one runtime object per row (probe
-- status, caches, kinds), named by the row's id and kept in step by the mirror.
--
-- id is a UUIDv7 minted by appdb.NewID (a cloud row copies the cloud's id).
-- source says how the row was created and how its credentials resolve:
-- 'kubeconfig' is a context in this device's kubeconfig and source_key its name;
-- 'cloud' is a cluster in the user's cloud account, with a NULL source_key.
-- UNIQUE (source, source_key) claims a context once; NULLs are distinct in a
-- unique index, so cloud rows are not constrained by it.
--
-- name is the display name the user set; NULL shows the context name instead.
-- enabled and sync_enabled default on, so a found context appears in the picker
-- without being switched on first; monitoring_enabled defaults off, since it lets
-- the background monitor spend model calls on the cluster.
-- updated_at moves on every user edit and never on a reconcile.
--
-- delete_requested_at is set when the user deletes the cluster. The row stays,
-- marked, until the runtime object and its caches are gone and no chat references
-- it; meanwhile it holds its (source, source_key) claim, so the importer cannot
-- re-create the context mid-teardown. The mirror deletes the row last.
--
-- Credentials are NOT stored here: a kubeconfig row resolves them through its
-- context.
CREATE TABLE clusters (
  id                  TEXT    PRIMARY KEY,
  source              TEXT    NOT NULL CHECK (source IN ('kubeconfig', 'cloud')),
  source_key          TEXT,
  name                TEXT,
  enabled             INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  sync_enabled        INTEGER NOT NULL DEFAULT 1 CHECK (sync_enabled IN (0, 1)),
  monitoring_enabled  INTEGER NOT NULL DEFAULT 0 CHECK (monitoring_enabled IN (0, 1)),
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL,
  delete_requested_at INTEGER,
  UNIQUE (source, source_key)
) STRICT, WITHOUT ROWID;

-- chats: a thread of messages and the runs they triggered. mode is which of
-- the app's two modes lists it, fixed at creation and checked by the column; cluster_id
-- is the clusters row it was started under, fixed at creation too. The cascade is the
-- backstop: the sweeper empties a marked cluster before its row goes. A chat has no
-- dialect of its own: each run records the one it ran on. title is NULL until
-- something sets one; the service reads it as ''. sandbox_disabled is the user's switch:
-- 1 runs the chat's commands outside the sandbox, each asking first. network_enabled is
-- the other: 1 gives the chat's sandboxed commands the internet. updated_at moves when a
-- message is posted or a run settles, never on a reconcile or a switch.
CREATE TABLE chats (
  id               TEXT    PRIMARY KEY,
  cluster_id       TEXT    NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
  mode             TEXT    NOT NULL CHECK (mode IN ('chat', 'dashboard')),
  title            TEXT,
  sandbox_disabled INTEGER NOT NULL DEFAULT 0 CHECK (sandbox_disabled IN (0, 1)),
  network_enabled  INTEGER NOT NULL DEFAULT 0 CHECK (network_enabled IN (0, 1)),
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- agent_runs: one execution of an agent. A chat run answers the user message named
-- by trigger_message_id — UNIQUE, so a message starts at most one run however many
-- times its request is replayed; the request's own idempotency is messages.request_key.
-- Plain REFERENCES both ways between messages and runs: the pair goes with its
-- chat and neither is deleted alone.
--
-- provider / model / effort are what was requested; effort is NULL where the model
-- has no such knob. dialect is the llm.Dialect of the provider the run was sent to,
-- which reads the run's stored content (its citations and payloads) after that
-- provider has left the catalog. Its CHECK refuses the empty string alone: the set
-- of dialects is llm's to list. app_version is the build that ran, and agent_type names the
-- agent definition (prompt, tools) that ran, which lives in code; together they say
-- what produced the run.
--
-- Lifecycle: queued -> running -> succeeded | failed | cancelled, and running <->
-- waiting_approval once a mutating tool call can wait on the user. The chat turn's
-- goroutine claims its own queued run (UPDATE ... WHERE status = 'queued'); there is
-- no worker queue yet. A cancel that lands before the claim settles the run cancelled
-- from queued. On startup every run still queued, running or waiting_approval is
-- failed: the process that owned it is gone and nothing resumes a queued run.
-- A child agent's run (trigger 'agent') is inserted running under its parent_run_id
-- and the parent's chat, with the task it was handed; result is its final
-- text. A monitor's run (trigger 'monitor') is inserted queued under its
-- cluster, with no chat and no message; task is its brief and result its
-- report. cluster_id is set on a monitor's run alone: a chat's and a subagent's
-- cluster is their chat's.
CREATE TABLE agent_runs (
  id              TEXT    PRIMARY KEY,
  parent_run_id   TEXT    REFERENCES agent_runs(id) ON DELETE CASCADE,
  agent_type      TEXT    NOT NULL,
  app_version     TEXT    NOT NULL,
  trigger         TEXT    NOT NULL CHECK (trigger IN ('chat', 'monitor', 'agent')),
  chat_id         TEXT    REFERENCES chats(id) ON DELETE CASCADE,
  cluster_id      TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  trigger_message_id
                  TEXT    UNIQUE REFERENCES messages(id),

  provider        TEXT    NOT NULL,
  model           TEXT    NOT NULL,
  effort          TEXT,
  dialect         TEXT    NOT NULL CHECK (dialect <> ''),

  task            TEXT,
  result          TEXT,
  error           TEXT,
  status          TEXT    NOT NULL DEFAULT 'queued'
                          CHECK (status IN ('queued', 'running', 'waiting_approval',
                                            'succeeded', 'failed', 'cancelled')),

  created_at      INTEGER NOT NULL,
  started_at      INTEGER,
  finished_at     INTEGER,

  CHECK ((trigger = 'monitor') = (chat_id IS NULL)),
  CHECK ((trigger = 'monitor') = (cluster_id IS NOT NULL))
) STRICT;

CREATE INDEX agent_runs_parent_idx ON agent_runs (parent_run_id);
CREATE INDEX agent_runs_cluster_idx ON agent_runs (cluster_id);
CREATE INDEX agent_runs_chat_idx   ON agent_runs (chat_id);
CREATE INDEX agent_runs_queued_idx ON agent_runs (id) WHERE status = 'queued';
-- The chat list marks a chat with a run waiting on the user, on every read.
CREATE INDEX agent_runs_waiting_idx ON agent_runs (chat_id) WHERE status = 'waiting_approval';

-- messages: the transcript. A message is what a client posts; a run is what the
-- server does about it, so a message's streaming/failed state is its run's status
-- and there is no status column here.
--
-- content is the content blocks exactly as they went over the wire, as JSON.
-- seq is the transcript order: a per-chat counter the posting transaction
-- assigns from MAX(seq). The single writer serializes it, so it needs no clock; ids
-- are identity alone. request_key is the client's idempotency key on every message a
-- client posts — a UUID the client minted, so a retry finds its first attempt — and
-- NULL on messages the server writes. run_id is the run that produced an assistant
-- message; NULL on a user message, whose run is found through
-- agent_runs.trigger_message_id.
--
-- A post inserts the user message, its queued run and the assistant row with content
-- '[]' in one transaction, so a replayed key never finds half of them.
CREATE TABLE messages (
  id              TEXT    PRIMARY KEY,
  chat_id TEXT    NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  seq             INTEGER NOT NULL,
  role            TEXT    NOT NULL CHECK (role IN ('user', 'assistant')),
  content         TEXT    NOT NULL,
  request_key     TEXT    UNIQUE,
  run_id          TEXT    REFERENCES agent_runs(id),
  created_at      INTEGER NOT NULL,
  UNIQUE (chat_id, seq)
) STRICT;
CREATE INDEX messages_run_idx ON messages (run_id);

-- llm_calls: one row per request to a model provider, under the run that made it.
-- The source of truth for token usage. The row is inserted before the provider is
-- contacted and written whole again when the call ends, so a call in flight has a
-- NULL finished_at; the next start closes one the process died under with error
-- 'stranded'. seq is the call's position in its run, assigned by the writer: a
-- run's call order is (run_id, seq), never its ids, which are minted wherever the
-- run happens to be running.
--
-- provider / effort are what was sent; model is what the provider said it served,
-- else what was asked for. stop_reason is the provider's, set only when a reply
-- arrived. error is the client side's, as safe plain text: a provider refusal, a
-- dropped stream, 'cancelled', 'stranded'. request, response and cost_usd_micros
-- are reserved and NULL.
--
-- input_tokens counts UNCACHED input only; the total the model read is the three
-- input columns summed. The four token columns are NULL together when the provider
-- reported no usage, when the reply broke off, or when the report was inconsistent
-- (cache counts past the input); a reported zero is 0. server_uses is how many
-- times the provider ran each server tool for the call, a JSON object from the
-- tool's name to its count, holding the tools its usage reports; NULL where it
-- reports none.
--
-- first_chunk_at is the first text or thinking chunk, NULL on a call that showed
-- nothing before it ended.
CREATE TABLE llm_calls (
  id                 TEXT    PRIMARY KEY,
  run_id             TEXT    NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  seq                INTEGER NOT NULL,

  provider           TEXT    NOT NULL,
  model              TEXT    NOT NULL,
  effort             TEXT,
  request            TEXT,
  response           TEXT,
  stop_reason        TEXT,
  error              TEXT,

  input_tokens       INTEGER,
  cache_read_tokens  INTEGER,
  cache_write_tokens INTEGER,
  output_tokens      INTEGER,
  server_uses        TEXT,
  cost_usd_micros    INTEGER,

  started_at         INTEGER NOT NULL,
  first_chunk_at     INTEGER,
  finished_at        INTEGER
) STRICT;

CREATE UNIQUE INDEX llm_calls_run_idx ON llm_calls (run_id, seq);

-- tool_calls: every tool the model invoked — the audit trail of what touched the
-- cluster. The owning run is reached through llm_call_id, the model call that
-- asked. A call that runs is inserted 'running' immediately before it executes,
-- and nothing runs when that write fails; it is written whole again with its
-- result or error. A call the loop refused without running it (budget, not-run,
-- unknown-tool) is 'failed' with no started_at, written at settlement. arguments
-- is the call's input as JSON; result is what the model read, plain text, on every
-- closed row, a refusal included; error is a JSON object, the loop's refusal
-- ({"error":"timeout"}, {"error":"cancelled"}, {"error":"stranded"}) or
-- {"error":"tool"} for a tool's own error. seq is the call's position among
-- the tool_use blocks of the reply that asked, so (llm_call_id, seq) is the order
-- the model called them in, refused calls included.
--
-- is_mutating is what the app treated the call as when it ran, stored per call
-- because definitions change across app versions: 1 on a gated call the user was
-- asked about, and 0 on every other, a gated call whose tool skipped the question
-- included. A gated call's first write is 'awaiting_approval' with no started_at,
-- in one transaction with its approvals row; it is written 'running' with
-- started_at the moment before the tool runs — so a NULL started_at says the call
-- never ran; 'denied' is the user's no. tool_use_id is the provider's id of the tool_use block the call
-- answers, what joins a row to its block. spawned_run_id is the child run an
-- Agent call created, written with the run's insert in one transaction, and
-- NULL on every other call.
--
-- cwd is the absolute directory a gated call starts in, as its tool resolved it
-- at the gate, written with the call's first row and never changed; '' on a call
-- that did not reach the gate and on a tool that runs nowhere. It is stored
-- because the home it was resolved against can change between runs. sandboxed
-- is whether a sandbox confined the call, written and kept the same way: whether
-- the machine had one is not in the arguments. network is the network a sandboxed
-- call ran with — 'chat' for the chat's switch, 'turn' for the turn's toggle,
-- 'approved' for its own request — NULL for none and NULL until it runs: it is written
-- on the row that marks the call running, so a call that asked and was denied keeps
-- NULL. Everything else a call does is read again from arguments by its own tool.
--
-- tool_name is the tool's name in the box and contract_name whose shapes it
-- uses: a tool of ours by the name it is offered under, with contract_name NULL,
-- since its shapes are its own definition; a vendor's by its own name, by
-- default its contract ('anthropic_web_search_20260318'), never a provider's
-- wire word.
--
-- runs_on is who ran the call: 'sidecar' for every call above, 'provider' for a
-- call the model's provider ran on its own servers (a web search). A provider
-- row is written the moment the stream shows the call: tool_use_id is the
-- provider's id, arguments what it sent, and no result, since the provider's
-- results are the model's alone. status, started_at and finished_at are the
-- sidecar's lifecycle, so a provider row has none of them: the app ran nothing,
-- and the CHECKs below hold both shapes. It is never gated. seq counts it among
-- the reply's calls: a reply's server calls all come before the tool_use blocks
-- that end it.
CREATE TABLE tool_calls (
  id             TEXT    PRIMARY KEY,
  llm_call_id    TEXT    NOT NULL REFERENCES llm_calls(id) ON DELETE CASCADE,
  seq            INTEGER NOT NULL,
  runs_on        TEXT    NOT NULL DEFAULT 'sidecar' CHECK (runs_on IN ('sidecar', 'provider')),

  tool_name      TEXT    NOT NULL,
  contract_name  TEXT,
  tool_use_id    TEXT,
  arguments      TEXT,
  cwd            TEXT    NOT NULL DEFAULT '',
  sandboxed      INTEGER NOT NULL DEFAULT 0 CHECK (sandboxed IN (0, 1)),
  network        TEXT    CHECK (network IN ('chat', 'turn', 'approved')),
  result         TEXT,
  error          TEXT,

  is_mutating    INTEGER NOT NULL DEFAULT 0 CHECK (is_mutating IN (0, 1)),
  spawned_run_id TEXT    REFERENCES agent_runs(id) ON DELETE SET NULL,
  status         TEXT    DEFAULT 'pending'
                         CHECK (status IN ('pending', 'awaiting_approval', 'running',
                                           'succeeded', 'failed', 'denied')),

  created_at     INTEGER NOT NULL,
  started_at     INTEGER,
  finished_at    INTEGER,

  CHECK ((runs_on = 'provider') = (status IS NULL)),
  CHECK (runs_on = 'sidecar' OR (started_at IS NULL AND finished_at IS NULL))
) STRICT;

CREATE UNIQUE INDEX tool_calls_llm_call_idx ON tool_calls (llm_call_id, seq);
CREATE INDEX tool_calls_spawned_idx  ON tool_calls (spawned_run_id);

-- approvals: the user's decisions on a tool call. A 'call' approval is the
-- decision on a mutating call, at most one per call, written with the call's
-- first row, before the approval request is shown; what the call does is its
-- tool_calls row's. The turn that waits on it writes the decision; a wait that
-- was cancelled or stranded leaves the row 'pending', the record of a question
-- nobody answered. An 'action' approval is a classified action a sandboxed
-- command's request asked for while its call ran — a change to the cluster —
-- held until the user decided, or 'abandoned' when its wait ended first;
-- request is the tools.ActionRequest as JSON, the action and the write's body
-- as sent. An action left 'pending' is one a crash stranded. An action the
-- permissions engine decided with nobody asked is 'allowed' or 'refused', and
-- reason names the mode or the rule that decided it. duration is how long an
-- approval holds, what the user chose: set on 'approved' alone, and 'once' on
-- a call's own.
CREATE TABLE approvals (
  id           TEXT    PRIMARY KEY,
  tool_call_id TEXT    NOT NULL REFERENCES tool_calls(id) ON DELETE CASCADE,
  kind         TEXT    NOT NULL DEFAULT 'call' CHECK (kind IN ('call', 'action')),
  request      TEXT,
  status       TEXT    NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending', 'approved', 'denied', 'abandoned', 'allowed', 'refused')),
  duration     TEXT    CHECK (duration IN ('once', 'command', 'chat', 'always')),
  reason       TEXT,
  created_at   INTEGER NOT NULL,
  decided_at   INTEGER,

  CHECK ((kind = 'call') = (request IS NULL)),
  CHECK (status NOT IN ('abandoned', 'allowed', 'refused') OR kind = 'action'),
  CHECK (duration IS NULL OR status = 'approved')
) STRICT, WITHOUT ROWID;

CREATE UNIQUE INDEX approvals_call_idx ON approvals (tool_call_id) WHERE kind = 'call';
CREATE INDEX approvals_actions_idx ON approvals (tool_call_id) WHERE kind = 'action';
CREATE INDEX approvals_pending_idx ON approvals (id) WHERE status = 'pending';

-- chat_grants: the rules that last for one chat, each a permissions.Rule as
-- JSON, read on every decision and gone with the chat. Only Kstack writes it.
CREATE TABLE chat_grants (
  id         TEXT    PRIMARY KEY,
  chat_id    TEXT    NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  rule       TEXT    NOT NULL,
  created_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

CREATE INDEX chat_grants_chat_idx ON chat_grants (chat_id);

-- memories: notes kept for later chats. cluster_id NULL is a note for every cluster.
-- server_uid is the cluster's kube-system UID at the last write, NULL for a note for
-- every cluster or a cluster never identified.
CREATE TABLE memories (
  id          TEXT    PRIMARY KEY,
  cluster_id  TEXT    REFERENCES clusters(id) ON DELETE CASCADE,
  server_uid  TEXT,
  name        TEXT    NOT NULL,
  body        TEXT    NOT NULL,
  written_by  TEXT    NOT NULL CHECK (written_by IN ('model', 'user')),
  chat_id     TEXT    REFERENCES chats(id) ON DELETE SET NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  CHECK (cluster_id IS NOT NULL OR server_uid IS NULL)
) STRICT;

-- One name per scope.
CREATE UNIQUE INDEX memories_name ON memories (ifnull(cluster_id, ''), name);

-- background_tasks: one row per command started with run_in_background and per
-- agent an Agent call started, written running before either starts. output_path is
-- the file the call's result named: a command's output, an agent's report. status
-- ends exited (a command; exit_code when it could be read), completed or failed (an
-- agent that answered or ended on its own error), stopped (stopped_by: the model's
-- TaskStop, the user's Stop or a chat's delete, the app's own stop, or an agent's
-- approval request that went unanswered), or lost, which the next start writes on a
-- row a crash left running. A finished row with a NULL notified_at is a waiting
-- notice: it rides the chat's next question, and an exit, or an agent's end under a
-- user's send, also starts a turn of its own. A stop the model made is written
-- notified, since its TaskStop result told it.
CREATE TABLE background_tasks (
  id              TEXT    PRIMARY KEY,
  chat_id TEXT    NOT NULL REFERENCES chats(id) ON DELETE CASCADE,
  tool_call_id    TEXT    NOT NULL UNIQUE REFERENCES tool_calls(id) ON DELETE CASCADE,
  output_path     TEXT    NOT NULL,
  status          TEXT    NOT NULL CHECK (status IN ('running', 'exited', 'completed', 'failed', 'stopped', 'lost')),
  stopped_by      TEXT    CHECK (stopped_by IN ('model', 'user', 'app', 'unanswered')),
  exit_code       INTEGER,
  started_at      INTEGER NOT NULL,
  finished_at     INTEGER,
  notified_at     INTEGER
) STRICT, WITHOUT ROWID;

CREATE INDEX background_tasks_chat_idx ON background_tasks (chat_id);
