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

package statestores

import (
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(multiconfig))
}

// multiconfig ensures that actor state store bindings spread over several
// --config files all apply: each file binds one actor type, and the state of
// both bound types still lands in their own stores, over HTTP and gRPC.
type multiconfig struct {
	routing
}

func (m *multiconfig) Setup(t *testing.T) []framework.Option {
	m.txType = "TransactionActor"
	m.balType = "BalanceActor"
	m.plainType = "PlainActor"

	return m.setup(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: transactions
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["`+m.txType+`"]
`, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: balance
spec:
  actors:
    stateStores:
    - name: wallet-balance
      actorTypes: ["`+m.balType+`"]
`)
}
