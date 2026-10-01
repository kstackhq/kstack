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
	"os"
	"slices"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/safe"
)

var awsCLI = cli{
	provider: AWS,
	name:     "aws",
	// "Error loading SSO Token" is also a profile never signed in, which the same
	// `aws sso login` fixes.
	expired: phrases(
		"The SSO session associated with this profile has expired or is otherwise invalid",
		"Error loading SSO Token",
		"Error when retrieving token from sso",
		"ExpiredToken",
	),
}

// AWSCredential is one borrowed credential: the process-credentials JSON's fields,
// and the account those keys belong to. Static keys have no SessionToken or Expires.
type AWSCredential struct {
	AccessKeyID     string    `json:"AccessKeyId"`
	SecretAccessKey string    `json:"SecretAccessKey"`
	SessionToken    string    `json:"SessionToken"`
	Expires         time.Time `json:"Expiration"`
	Account         string    `json:"-"`
}

// AWS borrows a profile's credential through `aws configure export-credentials`,
// which the AWS CLI has from 2.9, and reads its account with the credential itself.
func (s *Store) AWS(ctx context.Context, profile string) (AWSCredential, error) {
	return borrowAs[AWSCredential](ctx, s, s.awsBorrowing(profile))
}

func (s *Store) awsBorrowing(profile string) borrowing {
	key := "aws:" + profile
	return borrowing{
		key:    key,
		id:     Key{AWS, profile},
		proves: true,
		fetch: func(ctx context.Context) (answer, error) {
			out, err := s.tool(ctx, awsCLI, nil, "configure", "export-credentials", "--profile", profile, "--format", "process")
			if err != nil {
				return answer{}, err
			}
			var c AWSCredential
			if json.Unmarshal(out, &c) != nil || c.AccessKeyID == "" || c.SecretAccessKey == "" {
				return answer{}, errors.New("credentials: aws answered no credential")
			}
			// Registered before anything else runs, so a failure that echoes the
			// keys is blanked. A slot of its own leaves the cached credential's
			// registered whatever this run comes to.
			safe.SetSecrets("read:"+key, c.SecretAccessKey, c.SessionToken)
			// The region picks the sts endpoint alone; one that cannot be read
			// leaves the CLI's default.
			region, _ := s.Region(ctx, profile)
			if c.Account, err = s.callerAccount(ctx, c, region); err != nil {
				return answer{}, err
			}
			return answer{
				value:   c,
				expires: s.expiresAt(c.Expires, defaultTTL),
				secret:  c.SecretAccessKey,
				secrets: []string{c.SecretAccessKey, c.SessionToken},
			}, nil
		},
	}
}

// callerAccount is the account c's keys belong to. sts runs with those keys as the
// only identity it can find — in the environment, which the CLI reads first, and no
// profile named — so the account is the one a request signed with them reaches, and
// never one the profile resolves to a second time.
func (s *Store) callerAccount(ctx context.Context, c AWSCredential, region string) (string, error) {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(awsIdentityVars, name)
	})
	env = append(env, "AWS_ACCESS_KEY_ID="+c.AccessKeyID, "AWS_SECRET_ACCESS_KEY="+c.SecretAccessKey)
	if c.SessionToken != "" {
		env = append(env, "AWS_SESSION_TOKEN="+c.SessionToken)
	}
	if region != "" {
		env = append(env, "AWS_REGION="+region)
	}
	out, err := s.tool(ctx, awsCLI, env, "sts", "get-caller-identity", "--output", "json")
	if err != nil {
		return "", err
	}
	var id struct{ Account string }
	if json.Unmarshal(out, &id) != nil || id.Account == "" {
		return "", errors.New("credentials: aws answered no account")
	}
	return id.Account, nil
}

// awsIdentityVars are the variables through which the AWS CLI could take an
// identity other than the keys callerAccount hands it.
var awsIdentityVars = []string{
	"AWS_PROFILE", "AWS_DEFAULT_PROFILE",
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_SECURITY_TOKEN",
	"AWS_CREDENTIAL_EXPIRATION", "AWS_REGION",
}

// Region is a profile's configured region, "" for none. It reads the config file
// alone, so it has no expiry of its own.
func (s *Store) Region(ctx context.Context, profile string) (string, error) {
	return borrowAs[string](ctx, s, borrowing{
		key: "region:" + profile,
		id:  Key{AWS, profile},
		fetch: func(ctx context.Context) (answer, error) {
			out, err := s.tool(ctx, awsCLI, nil, "configure", "get", "region", "--profile", profile)
			// `aws configure get` exits 1 for a value that is not set.
			var exit *exitError
			if errors.As(err, &exit) && exit.code == 1 {
				out, err = nil, nil
			}
			if err != nil {
				return answer{}, err
			}
			return answer{value: strings.TrimSpace(string(out)), expires: s.opt.now().Add(defaultTTL)}, nil
		},
	})
}
