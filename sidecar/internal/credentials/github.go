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

package credentials

import (
	"context"
	"errors"
	"strings"
)

// A GitHub token has no expiry; a logout ends it and `gh auth login` renews it, as
// a login command renews an SSO session, so a token gone reads as expired.
var githubCLI = cli{
	provider: GitHub,
	name:     "gh",
	expired:  phrases("no oauth token"),
}

// GitHub borrows the token gh holds for host. It reports no expiry, so it is kept
// for githubTTL, and borrowing again is how a logout is seen.
func (s *Store) GitHub(ctx context.Context, host string) (string, error) {
	return borrowAs[string](ctx, s, s.githubBorrowing(host))
}

func (s *Store) githubBorrowing(host string) borrowing {
	return borrowing{
		key:    "github:" + host,
		id:     Key{Provider: GitHub},
		proves: true,
		fetch: func(ctx context.Context) (answer, error) {
			out, err := s.tool(ctx, githubCLI, nil, "auth", "token", "--hostname", host)
			if err != nil {
				return answer{}, err
			}
			token := strings.TrimSpace(string(out))
			if token == "" {
				return answer{}, errors.New("credentials: gh answered no token")
			}
			return tokenAnswer(token, token, s.opt.now().Add(githubTTL)), nil
		},
	}
}
