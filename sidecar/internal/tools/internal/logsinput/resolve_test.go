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

package logsinput

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kstackhq/kstack/sidecar/internal/services/logs"
)

// Every refusal of the service reaches the model with the code it carries, and an
// unmirrored source with its index; any other error passes through.
func TestARefusalKeepsItsCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		want *Refused
	}{
		{logs.ErrTooMany, &Refused{Code: logs.CodeTooMany, Source: -1}},
		{logs.Refusal{Code: logs.CodeTooMany, Reason: "the cache answered more than Kstack reads"}, &Refused{Code: logs.CodeTooMany, Source: -1}},
		{logs.Refusal{Code: logs.CodeBadScope, Reason: "bad kind"}, &Refused{Code: logs.CodeBadScope, Source: -1}},
		{logs.Unmirrored{Refusal: logs.Refusal{Code: logs.CodeNotMirrored, Reason: "no jobs"}, Source: 2}, &Refused{Code: logs.CodeNotMirrored, Source: 2}},
		{logs.NoNodes{Refusal: logs.Refusal{Code: logs.CodeNoNodes, Reason: "no nodes"}}, &Refused{Code: logs.CodeNoNodes, Source: -1}},
	} {
		assert.Equal(t, tc.want, refusedBy(tc.err), tc.err.Error())
	}
	err := errors.New("db closed")
	assert.Same(t, err, refusedBy(err))
}
