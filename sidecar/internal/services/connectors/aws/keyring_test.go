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

package aws

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnEntryReadsBackAsItWasSaved(t *testing.T) {
	k := newFakeKeyring()
	want := entry{V: entryVersion, Refresh: "refresh-1", DPoPKey: "pem", ARN: "arn:aws:iam::123456789012:user/alice"}
	require.NoError(t, saveEntry(k, want))

	got, found, err := loadEntry(k)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, want, got)
}

func TestNoEntryIsNotFound(t *testing.T) {
	_, found, err := loadEntry(newFakeKeyring())
	require.NoError(t, err)
	require.False(t, found)
}

func TestAKeyringThatCannotBeReadIsAnError(t *testing.T) {
	k := newFakeKeyring()
	k.failGet(errBoom)
	_, _, err := loadEntry(k)
	require.ErrorIs(t, err, errBoom)
}
