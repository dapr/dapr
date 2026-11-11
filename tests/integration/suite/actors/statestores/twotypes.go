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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(twotypes))
}

// twotypes ensures that a single binding listing two actor types keeps the
// state of both in the bound store, through the HTTP and gRPC APIs, while
// the default actor state store and the other store get none of it.
type twotypes struct {
	routing
}

func (r *twotypes) Setup(t *testing.T) []framework.Option {
	r.txType = "TransactionActor"
	r.balType = "AuditActor"
	r.plainType = "PlainActor"

	return r.setup(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["`+r.txType+`", "`+r.balType+`"]
`)
}

func (r *twotypes) Run(t *testing.T, ctx context.Context) {
	r.app.WaitUntilRunning(t, ctx)

	for _, actorType := range []string{r.txType, r.balType} {
		t.Run(actorType+"/http", func(t *testing.T) {
			saveAndReadHTTP(t, ctx, r.app, actorType, "h1")
		})
		t.Run(actorType+"/grpc", func(t *testing.T) {
			saveAndReadGRPC(t, ctx, r.app, actorType, "g1")
		})
	}

	appID := r.app.AppID()
	assert.Equal(t, []string{
		appID + "||" + r.balType + "||g1||k1",
		appID + "||" + r.balType + "||h1||k1",
		appID + "||" + r.txType + "||g1||k1",
		appID + "||" + r.txType + "||h1||k1",
	}, r.txDB.StateKeys(t, ctx))
	assert.Empty(t, r.balDB.StateKeys(t, ctx))
	assert.Empty(t, r.defaultDB.StateKeys(t, ctx))
}
