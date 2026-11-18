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

package bash

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/run/session"
)

// awsTestCreds are the user's credentials, which no run may hold.
var awsTestCreds = aws.Credentials{
	AccessKeyID: "AKIAIOSFODNN7EXAMPLE", SecretAccessKey: "wJalrXUtnFEMIK7MDENGbPxRfiCYzzKEY",
	SessionToken: "FwoGZXIvYXdzEXAMPLESESSIONTOKENxyz",
}

// fakeAWSLogin is the user's AWS login for a test: known credentials and a
// region.
type fakeAWSLogin struct{ region string }

func (l fakeAWSLogin) Credentials(context.Context) (aws.Credentials, error) { return awsTestCreds, nil }
func (fakeAWSLogin) Drop()                                                  {}
func (l fakeAWSLogin) Region() string                                       { return l.region }

// awsMount mounts AWS on a fresh run directory at port 4100.
func awsMount(t *testing.T, region string) (CloudMount, CloudRun) {
	run := CloudRun{Port: 4100, Dir: t.TempDir(), Session: session.Session{Kind: session.Chat}}
	m, err := NewAWS(fakeAWSLogin{region}, nil).Mount(t.Context(), run)
	require.NoError(t, err)
	t.Cleanup(m.Grant.End)
	return m, run
}

// The mount sets its variables and no other, every value its own, and leaves
// the region out when the sandbox would refuse it.
func TestTheAWSMountSetsExactlyItsVariables(t *testing.T) {
	m, run := awsMount(t, "eu-west-1")
	assert.Equal(t, []string{
		"AWS_ENDPOINT_URL=http://127.0.0.1:4100",
		"AWS_ACCESS_KEY_ID=" + sandbox.AWSKeyIDPrefix + strings.ToUpper(m.Grant.Token()),
		"AWS_SECRET_ACCESS_KEY=" + sandbox.AWSSecretPlaceholder,
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE=" + filepath.Join(run.Dir, "aws", "config"),
		"AWS_REGION=eu-west-1",
		"AWS_DEFAULT_REGION=eu-west-1",
	}, m.Env)
	assert.Equal(t, permissions.AWS, m.Grant.Target())
	require.Len(t, m.Routes, 1)
	assert.Equal(t, "127.0.0.1:"+strconv.Itoa(run.Port), m.Routes[0].Host)

	for _, region := range []string{"", "cn-north-1"} {
		m, _ := awsMount(t, region)
		assert.NotContains(t, strings.Join(m.Env, "\n"), "REGION=", region)
	}
}

// The config file is the one constant, under the run directory.
func TestTheAWSConfigFileIsAConstant(t *testing.T) {
	_, run := awsMount(t, "eu-west-1")
	text, err := os.ReadFile(filepath.Join(run.Dir, "aws", "config"))
	require.NoError(t, err)
	assert.Equal(t, awsConfig, string(text))
}

// A run directory that already holds an aws folder fails the mount.
func TestAnAWSFolderAlreadyThereFailsTheMount(t *testing.T) {
	run := CloudRun{Port: 4100, Dir: t.TempDir()}
	require.NoError(t, os.Mkdir(filepath.Join(run.Dir, "aws"), 0o700))
	_, err := NewAWS(fakeAWSLogin{"eu-west-1"}, nil).Mount(t.Context(), run)
	assert.ErrorIs(t, err, fs.ErrExist)
}

// hungLogin is a login whose region read waits on release, as one reading the
// user's AWS config from a dead network mount would.
type hungLogin struct {
	fakeAWSLogin
	release chan struct{}
}

func (l hungLogin) Region() string {
	<-l.release
	return "eu-west-1"
}

// A mount whose region read hangs gives up when its context ends, having
// written nothing.
func TestAMountAbandonsAHungRegionRead(t *testing.T) {
	login := hungLogin{release: make(chan struct{})}
	t.Cleanup(func() { close(login.release) })
	run := CloudRun{Port: 4100, Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	mounted := make(chan error, 1)
	go func() {
		_, err := NewAWS(login, nil).Mount(ctx, run)
		mounted <- err
	}()
	assert.ErrorIs(t, testutil.Recv(t, mounted, "the mount giving up"), context.Canceled)
	assert.NoDirExists(t, filepath.Join(run.Dir, "aws"))
}

// The client AWS is reached through waits for an answer's headers no longer
// than awsAnswerTimeout, and leaves a stored encoding as stored.
func TestTheAWSClientBoundsAnAnswersWait(t *testing.T) {
	transport := NewAWS(fakeAWSLogin{}, nil).(*awsPlatform).client.Transport.(*http.Transport)
	assert.Equal(t, awsAnswerTimeout, transport.ResponseHeaderTimeout)
	assert.True(t, transport.DisableCompression)
}
