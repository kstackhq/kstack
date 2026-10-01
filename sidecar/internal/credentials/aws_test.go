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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheAWSBorrowRunsExportCredentials(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON(secretKey, "2026-09-30T13:00:00Z")})
	s := newTestStore(t, f, c)

	got, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, AWSCredential{
		AccessKeyID:     "ASIAEXAMPLEKEY000001",
		SecretAccessKey: secretKey,
		SessionToken:    "session-token-0123456789",
		Expires:         time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC),
		Account:         "111111111111",
	}, got)
	assert.Equal(t, []string{awsExport, awsRegion, awsCaller}, f.all())
	assert.Nil(t, f.env(awsExport), "the export runs in the sidecar's environment")
	assert.Equal(t, Valid, s.State(Key{AWS, "dev"}).Status)
}

// The account is the identity of the keys the borrow answered, never of the
// profile resolved again: sts runs with those keys and no profile, so nothing in
// the config or the environment can hand it another credential.
func TestTheAccountIsTheBorrowedCredentialsOwn(t *testing.T) {
	t.Setenv("AWS_PROFILE", "prod")
	t.Setenv("AWS_DEFAULT_PROFILE", "prod")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAPRODKEY000000001")
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON(secretKey, "2026-09-30T13:00:00Z")})
	f.on("aws sts get-caller-identity --profile dev --output json", reply{stdout: `{"Account":"999999999999"}`})
	f.on(awsRegion, reply{stdout: "eu-west-1\n"})
	s := newTestStore(t, f, c)

	got, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, "111111111111", got.Account)
	assert.Zero(t, f.count("aws sts get-caller-identity --profile dev --output json"), "sts never names the profile")

	assert.ElementsMatch(t, []string{
		"AWS_ACCESS_KEY_ID=ASIAEXAMPLEKEY000001",
		"AWS_SECRET_ACCESS_KEY=" + secretKey,
		"AWS_SESSION_TOKEN=session-token-0123456789",
		"AWS_REGION=eu-west-1",
	}, awsVars(f.env(awsCaller)), "the credential alone, and the profile's region for the endpoint")

	t.Run("static keys send no session token", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: `{"Version":1,"AccessKeyId":"AKIAEXAMPLEKEY000001","SecretAccessKey":"` + secretKey + `"}`})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{
			"AWS_ACCESS_KEY_ID=AKIAEXAMPLEKEY000001",
			"AWS_SECRET_ACCESS_KEY=" + secretKey,
		}, awsVars(f.env(awsCaller)))
	})
}

// awsVars is the AWS_ variables of an environment, so a failure never prints the
// rest of it.
func awsVars(env []string) []string {
	var vars []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "AWS_") {
			vars = append(vars, kv)
		}
	}
	return vars
}

func TestANewBorrowRederivesTheAccount(t *testing.T) {
	f, c := newFakeTools(), newClock()
	f.on(awsExport, reply{stdout: awsJSON(secretKey, "2026-09-30T13:00:00Z")})
	s := newTestStore(t, f, c)
	first, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	require.Equal(t, "111111111111", first.Account)

	f.on(awsExport, reply{stdout: awsJSON("rotated-secret-0123456789", "2026-09-30T14:00:00Z")})
	f.on(awsCaller, reply{stdout: `{"Account":"222222222222"}`})
	c.advance(time.Hour)
	second, err := s.AWS(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, "rotated-secret-0123456789", second.SecretAccessKey)
	assert.Equal(t, "222222222222", second.Account, "the account follows the credential")
	assert.Equal(t, 2, f.count(awsCaller), "once per credential")
}

func TestAnAccountThatCannotBeReadFailsTheBorrow(t *testing.T) {
	t.Run("an expiry line is ErrExpired", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		f.on(awsCaller, reply{stderr: "An error occurred (ExpiredToken) when calling the GetCallerIdentity operation\n", exit: 254})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		assert.ErrorIs(t, err, ErrExpired)
		assert.Equal(t, Expired, s.State(Key{AWS, "dev"}).Status)
	})

	t.Run("a failure that echoes the keys is blanked", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "2026-09-30T13:00:00Z")})
		f.on(awsCaller, reply{stderr: "ExpiredToken: rejected " + secretKey + " with session-token-0123456789\n", exit: 254})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		assert.ErrorIs(t, err, ErrExpired)
		detail := s.State(Key{AWS, "dev"}).Detail
		assert.NotContains(t, detail, secretKey)
		assert.NotContains(t, detail, "session-token-0123456789")
	})

	t.Run("an answer with no account", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stdout: awsJSON(secretKey, "")})
		f.on(awsCaller, reply{stdout: "{}"})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		assert.EqualError(t, err, "credentials: aws answered no account")
		assert.Equal(t, Status(""), s.State(Key{AWS, "dev"}).Status)
	})

	t.Run("an expired export never reaches sts", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stderr: "Error loading SSO Token\n", exit: 255})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		assert.ErrorIs(t, err, ErrExpired)
		assert.Zero(t, f.count(awsCaller))
	})
}

func TestRegion(t *testing.T) {
	f, c := newFakeTools(), newClock()
	s := newTestStore(t, f, c)
	region, err := s.Region(t.Context(), "dev")
	require.NoError(t, err)
	assert.Empty(t, region, "a profile with no region")

	c.advance(defaultTTL - time.Second)
	_, err = s.Region(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, 1, f.count(awsRegion))
	c.advance(time.Second)
	f.on(awsRegion, reply{stdout: "eu-west-1\n"})
	region, err = s.Region(t.Context(), "dev")
	require.NoError(t, err)
	assert.Equal(t, "eu-west-1", region)
}

func TestEachExpiryLineIsErrExpired(t *testing.T) {
	cases := map[string]string{
		"sso session": "The SSO session associated with this profile has expired or is otherwise invalid. To refresh this SSO session run aws sso login",
		"sso token":   "Error loading SSO Token: Token for my-sso does not exist",
		"sso fetch":   "Error when retrieving token from sso: Token has expired and refresh failed",
		"expired":     "An error occurred (ExpiredToken) when calling the GetCallerIdentity operation",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			f, c := newFakeTools(), newClock()
			f.on(awsExport, reply{stderr: "\n" + line + "\n", exit: 255})
			s := newTestStore(t, f, c)
			_, err := s.AWS(t.Context(), "dev")
			assert.ErrorIs(t, err, ErrExpired)
			st := s.State(Key{AWS, "dev"})
			assert.Equal(t, Expired, st.Status)
			assert.Equal(t, line, st.Detail)
		})
	}

	t.Run("a line off the table is an ordinary error", func(t *testing.T) {
		f, c := newFakeTools(), newClock()
		f.on(awsExport, reply{stderr: "The config profile (dev) could not be found\n", exit: 255})
		s := newTestStore(t, f, c)
		_, err := s.AWS(t.Context(), "dev")
		require.Error(t, err)
		assert.NotErrorIs(t, err, ErrExpired)
		assert.Equal(t, Status(""), s.State(Key{AWS, "dev"}).Status)
	})
}
