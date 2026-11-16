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

import (
	"fmt"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/socket"
)

// FaultStore serves store as a pluggable actor state store. It returns the
// process to start before the daprds, and the options that give a daprd the
// store; use them with WithNoDB, and on any daprd that joins later.
func FaultStore(t *testing.T, store *fault.Store) (*statestore.StateStore, []daprd.Option) {
	t.Helper()
	sock := socket.New(t)
	ss := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(store),
	)
	return ss, []daprd.Option{
		daprd.WithSocket(t, sock),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, ss.SocketName())),
	}
}
