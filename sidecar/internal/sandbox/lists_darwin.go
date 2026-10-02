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

package sandbox

// platformLists is what only macOS's sandbox reads and hides. /Applications
// holds app bundles, which are programs: Docker Desktop's kubectl is a link
// into one, and so is Xcode's developer directory.
var platformLists = Lists{
	System: []string{
		"/usr", "/bin", "/sbin", "/System", "/Library", "/Applications", "/private/etc", "/opt",
		"/nix/store", "/nix/var/nix/profiles", "/run/current-system",
	},
	Never: []string{"~/Library/Keychains"},
}

// brewVar is Homebrew's var, which the System folders take in and which holds
// its services' databases and logs.
var brewVar = []string{"/opt/homebrew/var", "/usr/local/var"}
