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

package metadata

import (
	"context"
	nethttp "net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(statestores))
}

// statestores ensures that the metadata API reports, over HTTP and gRPC, the
// state stores bound by spec.actors.stateStores and spec.workflow.stateStore
// with their actor types, and whether each store is usable: a bound store
// that is missing is reported as not usable, with the reason, until hot
// reload loads it.
type statestores struct {
	app       *actors.Actors
	balanceDB *sqlite.SQLite

	resDir string
}

func (s *statestores) Setup(t *testing.T) []framework.Option {
	txDB := sqlite.New(t,
		sqlite.WithName("wallet-transactions"),
		sqlite.WithCreateStateTables(),
	)
	wfDB := sqlite.New(t,
		sqlite.WithName("wallet-workflows"),
		sqlite.WithCreateStateTables(),
	)
	s.balanceDB = sqlite.New(t,
		sqlite.WithName("wallet-balance"),
		sqlite.WithCreateStateTables(),
	)

	// The resources directory starts without the wallet-balance store.
	s.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "wallet-transactions.yaml"), []byte(txDB.GetComponent(t)), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "wallet-workflows.yaml"), []byte(wfDB.GetComponent(t)), 0o600))

	s.app = actors.New(t,
		actors.WithActorTypes("TransactionActor", "BalanceActor"),
		actors.WithActorTypeHandler("TransactionActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithActorTypeHandler("BalanceActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithResourcesDir(s.resDir),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: statestores
spec:
  actors:
    stateStores:
    - name: wallet-transactions
      actorTypes: ["TransactionActor", "AuditActor"]
    - name: wallet-balance
      actorTypes: ["BalanceActor"]
  workflow:
    stateStore: wallet-workflows
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(txDB, wfDB, s.balanceDB, s.app),
	}
}

func (s *statestores) Run(t *testing.T, ctx context.Context) {
	s.app.WaitUntilRunning(t, ctx)

	dapr := s.app.Daprd()
	client := dapr.GRPCClient(t, ctx)
	wfType := "dapr.internal." + dapr.Namespace() + "." + dapr.AppID() + "."

	// The stores are sorted by name and their actor types are sorted. A bound
	// actor type which the app does not host is reported too.
	expected := func(balanceLoaded bool) []*daprd.MetadataActorRuntimeStateStore {
		balance := &daprd.MetadataActorRuntimeStateStore{Name: "wallet-balance", ActorTypes: []string{"BalanceActor"}, Usable: balanceLoaded}
		if !balanceLoaded {
			balance.Error = "the state store is not loaded"
		}
		return []*daprd.MetadataActorRuntimeStateStore{
			balance,
			{Name: "wallet-transactions", ActorTypes: []string{"AuditActor", "TransactionActor"}, Usable: true},
			{Name: "wallet-workflows", ActorTypes: []string{
				wfType + "activity", wfType + "executor", wfType + "retentioner", wfType + "workflow",
			}, Usable: true},
		}
	}

	grpcStateStores := func(t require.TestingT) []*daprd.MetadataActorRuntimeStateStore {
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		stores := make([]*daprd.MetadataActorRuntimeStateStore, 0, len(resp.GetActorRuntime().GetStateStores()))
		for _, store := range resp.GetActorRuntime().GetStateStores() {
			stores = append(stores, &daprd.MetadataActorRuntimeStateStore{
				Name:       store.GetName(),
				ActorTypes: store.GetActorTypes(),
				Usable:     store.GetUsable(),
				Error:      store.GetError(),
			})
		}
		return stores
	}

	t.Run("bound store missing", func(t *testing.T) {
		assert.Equal(t, expected(false), dapr.GetMetaActorRuntime(t, ctx).StateStores)
		assert.Equal(t, expected(false), grpcStateStores(t))
	})

	t.Run("bound store loaded by hot reload", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "wallet-balance.yaml"), []byte(s.balanceDB.GetComponent(t)), 0o600))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, expected(true), dapr.GetMetaActorRuntime(c, ctx).StateStores)
			assert.Equal(c, expected(true), grpcStateStores(c))
		}, 20*time.Second, 10*time.Millisecond)
	})
}
