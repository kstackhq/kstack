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

//go:build unix

package app

import (
	"net/http"

	"github.com/kstackhq/kstack/sidecar/internal/run/githubproxy"
	"github.com/kstackhq/kstack/sidecar/internal/services/awsauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/azureauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/gcpauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/githubauth"
	"github.com/kstackhq/kstack/sidecar/internal/services/kubeconfig"
	"github.com/kstackhq/kstack/sidecar/internal/tools/bash"
)

// builtCloudPlatforms are the cloud platforms this build reaches, in
// Settings' order. AWS reads each kube-context's profile through kc.
func builtCloudPlatforms(kc *kubeconfig.Service) []bash.CloudPlatform {
	return []bash.CloudPlatform{
		bash.NewAWS(kc.AWSProfile, newAWSLogin, nil),
		bash.NewGitHub(githubauth.New(), githubproxy.GitHub()),
		bash.NewGCP(gcpauth.New(nil), nil),
		bash.NewAzure(azureauth.New(), http.DefaultTransport),
	}
}

// newAWSLogin is a login for profile, "" for the SDK's default chain.
func newAWSLogin(profile string) bash.AWSLogin { return awsauth.New(profile) }
