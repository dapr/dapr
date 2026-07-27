/*
Copyright 2026 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workflow

import "sync/atomic"

// Holder is shared between the gRPC API and the workflow actors so both read
// the same atomic snapshot of the compiled policies.
type Holder struct {
	p atomic.Pointer[CompiledPolicies]
}

func NewHolder() *Holder {
	return &Holder{}
}

func (h *Holder) Load() *CompiledPolicies {
	if h == nil {
		return nil
	}
	return h.p.Load()
}

func (h *Holder) Store(p *CompiledPolicies) {
	if h == nil {
		return
	}
	h.p.Store(p)
}
