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

-- oauth_connections: one row per model provider the user signs in to with OAuth,
-- keyed by the catalog's provider id ('chatgpt'). It holds no token: the refresh
-- token is in the OS keyring, in an entry that names its sign-in.
--
-- client_id is the OAuth client the provider issued this installation, NULL until
-- one is registered and for a provider whose client is fixed in code. subject and
-- email are the account that last signed in, email for display alone; both stay
-- across a sign-out, so the next sign-in can be held to the same account.
--
-- session_id is a random value minted at each sign-in and written to the keyring
-- entry beside the refresh token; NULL is signed out. A session exists only when
-- the two match, so an entry and a row that a crash, a lost commit or a restored
-- backup left apart never make one.
--
-- provider_data is what one provider alone keeps, a JSON object in its service's
-- own shape (ChatGPT: {"hostID": ...}); NULL when it keeps nothing.
CREATE TABLE oauth_connections (
  provider      TEXT    PRIMARY KEY CHECK (provider <> ''),
  client_id     TEXT,
  subject       TEXT,
  email         TEXT,
  session_id    TEXT,
  provider_data TEXT,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT, WITHOUT ROWID;
