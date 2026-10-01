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

var azureCLI = cli{
	provider: Azure,
	name:     "az",
	expired: append(
		codes("AADSTS700082", "AADSTS70008", "AADSTS50173"),
		phrases("Interactive authentication is needed", "Please run 'az login'")...,
	),
}

// Azure borrows az's token for one resource. There is one Azure login across its
// resources, so every resource answers for one identity.
func (s *Store) Azure(ctx context.Context, resource string) (Token, error) {
	return borrowAs[Token](ctx, s, s.azureBorrowing(resource))
}

func (s *Store) azureBorrowing(resource string) borrowing {
	return borrowing{
		key:    "azure:" + resource,
		id:     Key{Provider: Azure},
		proves: true,
		fetch: func(ctx context.Context) (answer, error) {
			out, err := s.tool(ctx, azureCLI, nil, "account", "get-access-token", "--resource", resource, "--output", "json")
			if err != nil {
				return answer{}, err
			}
			// expires_on is a POSIX time from azure-cli 2.54; an older CLI prints only
			// the local-time expiresOn, which is not read.
			var got struct {
				AccessToken string `json:"accessToken"`
				ExpiresOn   *int64 `json:"expires_on"`
			}
			if json.Unmarshal(out, &got) != nil || got.AccessToken == "" {
				return answer{}, errors.New("credentials: az answered no token")
			}
			t := Token{Value: got.AccessToken}
			if got.ExpiresOn != nil {
				t.Expires = time.Unix(*got.ExpiresOn, 0).UTC()
			}
			return tokenAnswer(t, t.Value, s.expiresAt(t.Expires, defaultTTL)), nil
		},
	}
}
