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
	"encoding/json"
	"errors"
	"time"
)

var googleCLI = cli{
	provider: Google,
	name:     "gcloud",
	expired:  phrases("Reauthentication required", "Reauthentication failed", "invalid_grant"),
}

// Token is one bearer token and when it stops working, zero when the tool did not
// say.
type Token struct {
	Value   string
	Expires time.Time
}

// Google borrows gcloud's access token. `--min-expiry=15m` makes gcloud refresh a
// cached token with less than that left, so the cache never serves one about to end.
func (s *Store) Google(ctx context.Context) (Token, error) {
	return borrowAs[Token](ctx, s, s.googleBorrowing())
}

func (s *Store) googleBorrowing() borrowing {
	return borrowing{
		key:    "gcp",
		id:     Key{Provider: Google},
		proves: true,
		fetch: func(ctx context.Context) (answer, error) {
			out, err := s.tool(ctx, googleCLI, nil, "config", "config-helper", "--min-expiry=15m", "--format=json")
			if err != nil {
				return answer{}, err
			}
			var helper struct {
				Credential struct {
					AccessToken string    `json:"access_token"`
					TokenExpiry time.Time `json:"token_expiry"`
				} `json:"credential"`
			}
			if json.Unmarshal(out, &helper) != nil || helper.Credential.AccessToken == "" {
				return answer{}, errors.New("credentials: gcloud answered no token")
			}
			t := Token{Value: helper.Credential.AccessToken, Expires: helper.Credential.TokenExpiry}
			return tokenAnswer(t, t.Value, s.expiresAt(t.Expires, defaultTTL)), nil
		},
	}
}
