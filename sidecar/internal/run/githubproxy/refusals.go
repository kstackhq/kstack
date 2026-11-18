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

package githubproxy

// What the proxy answers a request it does not send, in the user's words.
// Each is a 403 unless it says otherwise, which neither gh nor git retries.
const (
	refusedHost         = "kstack: this host is not one of GitHub's routes."
	refusedPath         = "kstack: this path reads differently decoded or resolved, so it was not sent."
	refusedMethod       = "kstack: this method does not reach GitHub from the sandbox."
	refusedCredential   = "kstack: this request answers a credential, so the sandbox never sends it."
	refusedGraphQL      = "kstack: this GraphQL request cannot be read as GitHub would read it, so it was not sent."
	refusedGitRoute     = "kstack: only Git's smart HTTP protocol reaches GitHub from the sandbox."
	refusedPush         = "kstack: this push cannot be read, so it was not sent."
	refusedPushTooLarge = "kstack: this push names too many refs to show at once. Push fewer refs at a time."
	refusedLink         = "kstack: GitHub answered with a link to another host, which only a read the user approves may follow."
	refusedNoLogin      = "kstack: no GitHub login was found; run `gh auth login` outside Kstack."
	refusedUnreachable  = "kstack: GitHub could not be reached." // 502
)
