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
	"sync"
	"time"

	"github.com/kstackhq/kstack/sidecar/internal/run/awsproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/permissions"
	"github.com/kstackhq/kstack/sidecar/internal/run/runproxy"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
)

// AWSLogin is one of the user's AWS logins, resolved outside the sandbox
// (services/awsauth).
type AWSLogin interface {
	awsproxy.Identity
	Region() string
}

// AWSLogins are the user's AWS logins, and the profile a kube-context names.
type AWSLogins interface {
	// Profile is the AWS profile kubeContext's credentials plugin reaches AWS
	// as, "" for none (services/kubeconfig).
	Profile(kubeContext string) string
	// Login is profile's login; "" is the SDK's default chain.
	Login(profile string) AWSLogin
}

// awsAnswerTimeout is how long AWS may take to answer a request's headers.
const awsAnswerTimeout = 2 * time.Minute

// awsSettings are what every section of the run's AWS config file sets: no
// compressed body, which a grant does not read.
const awsSettings = "disable_request_compression = true\n"

// awsConfig is the run's AWS config file. Naming it keeps a client from
// reading a config in the workspace. The run's profile, if any, is declared
// so --profile parses, and holds the run's keys, since the AWS CLI ignores
// the environment's keys once --profile names a profile.
func awsConfig(profile, keyID string) string {
	keys := "aws_access_key_id = " + keyID + "\naws_secret_access_key = " + sandbox.AWSSecretPlaceholder + "\n"
	switch profile {
	case "":
		return "[default]\n" + awsSettings
	case "default":
		return "[default]\n" + keys + awsSettings
	}
	return "[default]\n" + awsSettings + "[profile " + profile + "]\n" + keys + awsSettings
}

// awsPlatform is AWS: a route at the loopback authority, since the client
// dials AWS_ENDPOINT_URL directly, over the re-signing proxy.
type awsPlatform struct {
	logins AWSLogins
	client *http.Client

	mu      sync.Mutex
	buckets map[string]*awsproxy.Buckets // each login's, by profile, shared by its runs
}

// NewAWS is AWS over logins, reaching AWS through client: nil is the
// sidecar's own network.
func NewAWS(logins AWSLogins, client *http.Client) CloudPlatform {
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
	return &awsPlatform{logins: logins, client: client, buckets: map[string]*awsproxy.Buckets{}}
}

func (*awsPlatform) Target() permissions.Target { return permissions.AWS }

// bucketsOf is profile's login's buckets.
func (p *awsPlatform) bucketsOf(profile string) *awsproxy.Buckets {
	p.mu.Lock()
	defer p.mu.Unlock()
	b, ok := p.buckets[profile]
	if !ok {
		b = awsproxy.NewBuckets()
		p.buckets[profile] = b
	}
	return b
}

// profileOf is the profile a run reaches AWS as: the one its cluster's
// kube-context names, else "" for the default chain. A name the config file
// cannot hold is none.
func (p *awsPlatform) profileOf(run CloudRun) string {
	if run.KubeContext == "" {
		return ""
	}
	profile := p.logins.Profile(run.KubeContext)
	if !sandbox.AWSProfile(profile) {
		return ""
	}
	return profile
}

// Mount writes the run's config file and answers the route, the variables
// and the grant, reaching AWS as the profile the run's cluster names. The
// key id carries the grant's token; the secret signs nothing AWS accepts. A
// region the sandbox would refuse is left out.
func (p *awsPlatform) Mount(ctx context.Context, run CloudRun) (CloudMount, error) {
	profile := p.profileOf(run)
	login := p.logins.Login(profile)
	// Region can read the user's AWS config from a network mount, so the read
	// is abandoned when ctx ends, before anything is written.
	regions := make(chan string, 1)
	go func() { regions <- login.Region() }()
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
	g := runproxy.NewGrant(permissions.AWS, run.Session, run.Asker, run.Refusal, cloudLimits)
	keyID := sandbox.AWSKeyIDPrefix + strings.ToUpper(g.Token())
	if err := writeNew(config, []byte(awsConfig(profile, keyID))); err != nil { // coverage:ignore the folder was made empty just now
		g.End()
		return CloudMount{}, err
	}
	authority := "127.0.0.1:" + strconv.Itoa(run.Port)
	env := []string{
		"AWS_ENDPOINT_URL=http://" + authority,
		"AWS_ACCESS_KEY_ID=" + keyID,
		"AWS_SECRET_ACCESS_KEY=" + sandbox.AWSSecretPlaceholder,
		"AWS_EC2_METADATA_DISABLED=true",
		"AWS_CONFIG_FILE=" + config,
	}
	if sandbox.AWSRegion(region) {
		env = append(env, "AWS_REGION="+region, "AWS_DEFAULT_REGION="+region)
	}
	proxy := awsproxy.New(g, awsproxy.Upstream{Identity: login, Client: p.client, Buckets: p.bucketsOf(profile)})
	return CloudMount{Routes: []Route{{Host: authority, Handler: proxy}}, Env: env, Grant: g}, nil
}
