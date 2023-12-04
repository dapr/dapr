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

package invoke

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(igrpc))
}

type igrpc struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	daprd3 *daprd.Daprd
	place  *placement.Placement
}

func (i *igrpc) Setup(t *testing.T) []framework.Option {
	i.place = placement.New(t)

	newHandler := func(id int) *prochttp.HTTP {
		handler := http.NewServeMux()
		handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"entities": ["myactortype%d"]}`, id)
		})
		handler.HandleFunc(fmt.Sprintf("/actors/myactortype%d/myactorid/method/invoke", id), func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(strconv.Itoa(id)))
		})
		handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`OK`))
		})
		handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

		return prochttp.New(t, prochttp.WithHandler(handler))
	}

	daprdopts := func(srv *prochttp.HTTP) []daprd.Option {
		return []daprd.Option{
			daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: actorStateStore
    value: true
`),
			daprd.WithPlacementAddresses("localhost:" + strconv.Itoa(i.place.Port())),
			daprd.WithAppProtocol("http"),
			daprd.WithAppPort(srv.Port()),
			daprd.WithAppHealthCheck(true),
		}
	}

	srv1, srv2, srv3 := newHandler(0), newHandler(1), newHandler(2)
	i.daprd1 = daprd.New(t, daprdopts(srv1)...)
	i.daprd2 = daprd.New(t, daprdopts(srv2)...)
	i.daprd3 = daprd.New(t, daprdopts(srv3)...)

	return []framework.Option{
		framework.WithProcesses(i.place, srv1, srv2, srv3, i.daprd1, i.daprd2, i.daprd3),
	}
}

func (i *igrpc) Run(t *testing.T, ctx context.Context) {
	i.place.WaitUntilRunning(t, ctx)
	i.daprd1.WaitUntilRunning(t, ctx)
	i.daprd2.WaitUntilRunning(t, ctx)
	i.daprd3.WaitUntilRunning(t, ctx)

	getClient := func(daprd *daprd.Daprd) rtv1.DaprClient {
		conn, err := grpc.DialContext(ctx,
			fmt.Sprintf("localhost:%d", daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return rtv1.NewDaprClient(conn)
	}

	client1, client2, client3 := getClient(i.daprd1), getClient(i.daprd2), getClient(i.daprd3)

	for i, client := range []rtv1.DaprClient{client1, client2, client3} {
		i := i
		client := client
		assert.EventuallyWithT(t, func(t *assert.CollectT) {
			_, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype" + strconv.Itoa(i),
				ActorId:   "myactorid",
				Method:    "invoke",
			})
			//nolint:testifylint
			assert.NoError(t, err)
		}, time.Second*15, time.Millisecond*100)
	}

	for _, client := range []rtv1.DaprClient{client1, client2, client3} {
		for target := 0; target < 3; target++ {
			resp, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "myactortype" + strconv.Itoa(target),
				ActorId:   "myactorid",
				Method:    "invoke",
			})
			require.NoError(t, err)
			assert.Equal(t, strconv.Itoa(target), string(resp.GetData()))
		}
	}
}
