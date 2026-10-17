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

package logs

import "context"

// kubelet is the Backend that reads each log as the kubelet serves it, through
// the API server, over get.
type kubelet struct{ get getter }

func newKubelet(get getter) Backend { return kubelet{get} }

func (k kubelet) Forward(ctx context.Context, st stream, o readOpts) *streamRead {
	return readForward(ctx, k.get, st, o)
}

func (k kubelet) Backward(ctx context.Context, st stream, o readOpts) *streamRead {
	return readBackward(ctx, k.get, st, o)
}

func (k kubelet) Tail(ctx context.Context, st stream, o readOpts) *streamRead {
	o.follow = true
	return readForward(ctx, k.get, st, o)
}
