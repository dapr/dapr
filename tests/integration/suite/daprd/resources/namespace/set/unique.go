/*
Copyright 2024 The Dapr Authors
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

package set

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(unique))
}

// unique tests that components loaded in self hosted mode in the same
// namespace and same name will cause a component conflict.
type unique struct {
	daprd *daprd.Daprd

	logline *logline.LogLine
}

func (u *unique) Setup(t *testing.T) []framework.Option {
	u.logline = logline.New(t, logline.WithStdoutLineContains(
		`Fatal error from runtime: failed to load components: duplicate definition of Component name abc (pubsub.in-memory/v1) with existing abc (state.in-memory/v1)\nduplicate definition of Component name 123 (state.in-memory/v1) with existing 123 (state.in-memory/v1)\nduplicate definition of Component name 123 (pubsub.in-memory/v1) with existing 123 (state.in-memory/v1)`,
	))

	u.daprd = daprd.New(t, daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: abc
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: abc
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: abc
 namespace: foobar
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
 namespace: mynamespace
spec:
 type: state.in-memory
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 123
spec:
 type: pubsub.in-memory
 version: v1
`),
		daprd.WithNamespace("mynamespace"),
		daprd.WithExit1(),
		daprd.WithLogLineStdout(u.logline),
	)
	return []framework.Option{
		framework.WithProcesses(u.logline, u.daprd),
	}
}

func (u *unique) Run(t *testing.T, ctx context.Context) {
	u.logline.EventuallyFoundAll(t)
}
