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

package resources

import (
	"context"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(versionv2))
}

// versionv2 tests dapr will load v1 and v2 components.
type versionv2 struct {
	proc *procdaprd.Daprd
}

func (v *versionv2) Setup(t *testing.T) []framework.Option {
	v.proc = procdaprd.New(t, procdaprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem-v1
spec:
  type: state.in-memory
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem-omitted
spec:
  type: state.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: etcd-v1
spec:
  type: state.etcd
  version: v1
  metadata:
    - name: endpoints
      value: "localhost:12379"
    - name: keyPrefixPath
      value: "daprv1"
    - name: tlsEnable
      value: "false"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: etcd-v2
spec:
  type: state.etcd
  version: v2
  metadata:
    - name: endpoints
      value: "localhost:12379"
    - name: keyPrefixPath
      value: "daprv2"
    - name: tlsEnable
      value: "false"
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: etcd-omitted
spec:
  type: state.etcd
  metadata:
    - name: endpoints
      value: "localhost:12379"
    - name: keyPrefixPath
      value: "daprv1"
    - name: tlsEnable
      value: "false"
`))
	return []framework.Option{
		framework.WithProcesses(v.proc),
	}
}

func (v *versionv2) Run(t *testing.T, ctx context.Context) {
	v.proc.WaitUntilRunning(t, ctx)
}
