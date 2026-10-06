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

//go:build !windows

package logging

import (
	"os"
	"path/filepath"
	"testing"
)

// The log holds cluster names and server hostnames. Windows has no mode bits: there the
// inherited profile ACL is the whole protection.
func TestOpenCreatesAnOwnerOnlyFileAndDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	path := filepath.Join(dir, "sidecar.log")

	if _, _, err := open(t, path, false); err != nil {
		t.Fatalf("Open: %v", err)
	}

	for _, tt := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{"directory", dir, 0o700},
		{"file", path, 0o600},
	} {
		info, err := os.Stat(tt.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tt.name, err)
		}
		if got := info.Mode().Perm(); got != tt.want {
			t.Errorf("%s mode = %#o, want %#o", tt.name, got, tt.want)
		}
	}
}
