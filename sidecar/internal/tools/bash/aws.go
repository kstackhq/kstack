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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/run/awsproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// AWSLogin is the user's AWS login, resolved outside the sandbox
// (services/awsauth).
type AWSLogin interface {
	awsproxy.Identity
	Region() string
}

// awsAnswerTimeout is how long AWS may take to answer a request's headers.
const awsAnswerTimeout = 2 * time.Minute

// awsConfig is the run's AWS config file: it asks for no compressed body,
// which a grant does not read, and naming it keeps a client from reading a
// config in the workspace.
const awsConfig = "[default]\ndisable_request_compression = true\n"

// awsPlatform is AWS: a route at the loopback authority, since the client
// dials AWS_ENDPOINT_URL directly, over the re-signing proxy.
type awsPlatform struct {
	login   AWSLogin
	client  *http.Client
	buckets *awsproxy.Buckets // the login's, shared by every run
}

// NewAWS is AWS over login, reaching AWS through client: nil is the
// sidecar's own network.
func NewAWS(login AWSLogin, client *http.Client) CloudPlatform {
	if client == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		// An object stored gzipped passes as stored, so the client's checksum
		// of it holds.
		transport.DisableCompression = true
		// An endpoint that accepts and never answers would hold the grant's
		// slots for the run; a slow download's body still streams.
		transport.ResponseHeaderTimeout = awsAnswerTimeout
		client = &http.Client{Transport: transport}
	}
	return &awsPlatform{login: login, client: client, buckets: awsproxy.NewBuckets()}
}

func (*awsPlatform) Target() permissions.Target { return permissions.AWS }

// Mount writes the run's config file and answers the route, the variables
// and the grant. The key id carries the grant's token; the secret signs
// nothing AWS accepts. A region the sandbox would refuse is left out.
func (p *awsPlatform) Mount(ctx context.Context, run CloudRun) (CloudMount, error) {
	// Region can read the user's AWS config from a network mount, so the read
	// is abandoned when ctx ends, before anything is written.
	regions := make(chan string, 1)
	go func() { regions <- p.login.Region() }()
	var region string
	select {
	case region = <-regions:
	case <-ctx.Done():
		return CloudMount{}, ctx.Err()
	}
	config := sandbox.AWSConfigPath(run.Dir)
	if err := os.Mkdir(filepath.Dir(config), 0o700); err != nil {
		return CloudMount{}, err
	}
	if err := writeNew(config, []byte(awsConfig)); err != nil { // coverage:ignore the folder was made empty just now
		return CloudMount{}, err
	}
	g := runproxy.NewGrant(permissions.AWS, run.Session, run.Asker, run.Refusal, cloudLimits)
	authority := "127.0.0.1:" + strconv.Itoa(run.Port)
	env := []string{
		"AWS_ENDPOINT_URL=http://" + authority,
		"AWS_ACCESS_KEY_ID=" + sandbox.AWSKeyIDPrefix + strings.ToUpper(g.Token()),
		"AWS_SECRET_ACCESS_KEY=" + sandbox.AWSSecretPlaceholder,
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE=" + config,
	}
	if sandbox.AWSRegion(region) {
		env = append(env, "AWS_REGION="+region, "AWS_DEFAULT_REGION="+region)
	}
	proxy := awsproxy.New(g, awsproxy.Upstream{Identity: p.login, Client: p.client, Buckets: p.buckets})
	return CloudMount{Routes: []Route{{Host: authority, Handler: proxy}}, Env: env, Grant: g}, nil
}
