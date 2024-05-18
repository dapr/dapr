/*
Copyright 2023 The Dapr Authors
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

package framework

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process"
)

type options struct {
	procs []process.Interface
}

// Option is a function that configures the Framework's options.
type Option func(*options)

func Run(t *testing.T, ctx context.Context, opts ...Option) {
	t.Helper()

	o := options{}
	for _, opt := range opts {
		opt(&o)
	}

	t.Logf("starting %d processes", len(o.procs))

	for i, proc := range o.procs {
		i := i
		proc.Run(t, ctx)
		t.Cleanup(func() { o.procs[i].Cleanup(t) })
	}
}
