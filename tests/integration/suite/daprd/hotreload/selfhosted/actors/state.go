/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actors

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	contribstate "github.com/dapr/components-contrib/state"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(state))
}

type state struct {
	daprd *daprd.Daprd
	place *placement.Placement

	resDir string
}

func (s *state) Setup(t *testing.T) []framework.Option {
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true
`), 0o600))

	s.resDir = t.TempDir()

	handler := http.NewServeMux()
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.URL.Path)
	})
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	srv := prochttp.New(t, prochttp.WithHandler(handler))

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`), 0o600))

	s.place = placement.New(t)
	s.daprd = daprd.New(t,
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(s.resDir),
		daprd.WithPlacementAddresses(s.place.Address()),
		daprd.WithAppPort(srv.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(srv, s.place, s.daprd),
	}
}

func (s *state) Run(t *testing.T, ctx context.Context) {
	s.place.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilRunning(t, ctx)

	client := s.daprd.GRPCClient(t, ctx)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Method:    "helloworld",
		})
		//nolint:testifylint
		if assert.NoError(c, err) {
			assert.Equal(c, "/actors/myactortype/myactorid/method/helloworld", string(resp.GetData()))
		}
	}, time.Second*10, time.Millisecond*100)

	require.Len(t, util.GetMetaComponents(t, ctx, util.HTTPClient(t), s.daprd.HTTPPort()), 2)

	_, err := client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: string(contribstate.OperationUpsert),
				Key:           "mykey",
				Value:         &anypb.Any{Value: []byte("myvalue")},
			},
		},
	})
	require.NoError(t, err)
	resp, err := client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "myactortype", ActorId: "myactorid", Key: "mykey",
	})
	require.NoError(t, err)
	assert.Equal(t, "myvalue", string(resp.GetData()))

	require.NoError(t, os.Remove(filepath.Join(s.resDir, "1.yaml")))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), s.daprd.HTTPPort()), 1)
	}, time.Second*5, time.Millisecond*100)

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: string(contribstate.OperationUpsert),
				Key:           "mykey",
				Value:         &anypb.Any{Value: []byte("myvalue")},
			},
		},
	})
	require.ErrorContains(t, err, "error saving actor transaction state: actors: state store does not exist or incorrectly configured")

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
`), 0o600))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, util.GetMetaComponents(t, ctx, util.HTTPClient(t), s.daprd.HTTPPort()), 2)
	}, time.Second*5, time.Millisecond*100)

	_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Operations: []*rtv1.TransactionalActorStateOperation{
			{
				OperationType: string(contribstate.OperationUpsert),
				Key:           "mykey",
				Value:         &anypb.Any{Value: []byte("myvalue")},
			},
		},
	})
	require.ErrorContains(t, err, "error saving actor transaction state: actors: state store does not exist or incorrectly configured")

	require.NoError(t, os.WriteFile(filepath.Join(s.resDir, "1.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: inmem
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`), 0o600))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.ExecuteActorStateTransaction(ctx, &rtv1.ExecuteActorStateTransactionRequest{
			ActorType: "myactortype",
			ActorId:   "myactorid",
			Operations: []*rtv1.TransactionalActorStateOperation{
				{
					OperationType: string(contribstate.OperationUpsert),
					Key:           "mykey",
					Value:         &anypb.Any{Value: []byte("newvalue")},
				},
			},
		})
		//nolint:testifylint
		assert.NoError(c, err)
	}, time.Second*5, time.Millisecond*100)
	resp, err = client.GetActorState(ctx, &rtv1.GetActorStateRequest{
		ActorType: "myactortype", ActorId: "myactorid", Key: "mykey",
	})
	require.NoError(t, err)
	assert.Equal(t, "newvalue", string(resp.GetData()))
}
