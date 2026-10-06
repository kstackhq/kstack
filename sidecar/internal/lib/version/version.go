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

// Package version is the build's version, stamped by the linker. Nothing here reads
// the environment or a file: what a record says it was written by is what the
// binary was built as.
package version

// Version is the release version, set with
// -X github.com/kstackhq/kstack/sidecar/internal/lib/version.Version=<v>
// by scripts/build-sidecar.go when SIDECAR_VERSION is given. dev otherwise.
var Version = "dev"
