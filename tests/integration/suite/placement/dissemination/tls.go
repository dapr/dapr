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

package dissemination

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(tls))
}

type tls struct {
	sentry                 *sentry.Sentry
	place                  *placement.Placement
	daprd1, daprd2, daprd3 *daprd.Daprd
	srv1, srv2, srv3       *prochttp.HTTP
}

func (n *tls) Setup(t *testing.T) []framework.Option {
	n.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, n.sentry.CABundle().X509.TrustAnchors, 0o600))
	n.place = placement.New(t,
		placement.WithEnableTLS(true),
		placement.WithSentryAddress(n.sentry.Address()),
		placement.WithTrustAnchorsFile(taFile),
		placement.WithMetadataEnabled(true),
	)

	handler1 := http.NewServeMux()
	handler1.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		types := []string{"actor1"}
		fmt.Fprintf(w, `{"entities": ["%s"]}`, strings.Join(types, `","`))
	})
	handler1.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler1.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK1`))
	})

	handler2 := http.NewServeMux()
	handler2.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		types := []string{"actor2"}
		fmt.Fprintf(w, `{"entities": ["%s"]}`, strings.Join(types, `","`))
	})
	handler2.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler2.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK2`))
	})

	handler3 := http.NewServeMux()
	handler3.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		types := []string{"actor1", "actor3"}
		// "actor1" exists in both app 1 and app3, but the apps are in a different namespace
		fmt.Fprintf(w, `{"entities": ["%s"]}`, strings.Join(types, `","`))
	})
	handler3.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler3.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`OK3`))
	})

	n.srv1 = prochttp.New(t, prochttp.WithHandler(handler1))
	n.srv2 = prochttp.New(t, prochttp.WithHandler(handler2))
	n.srv3 = prochttp.New(t, prochttp.WithHandler(handler3))

	n.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore1"),
		daprd.WithAppID("my-app1"),
		daprd.WithNamespace("ns1"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(n.sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(n.sentry.Address()),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithAppPort(n.srv1.Port()),
	)

	n.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore2"),
		daprd.WithAppID("my-app2"),
		daprd.WithNamespace("ns2"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(n.sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(n.sentry.Address()),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithAppPort(n.srv2.Port()),
	)

	n.daprd3 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore3"),
		daprd.WithAppID("my-app3"),
		daprd.WithNamespace("ns2"),
		daprd.WithMode("standalone"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_TRUST_ANCHORS", string(n.sentry.CABundle().X509.TrustAnchors))),
		daprd.WithSentryAddress(n.sentry.Address()),
		daprd.WithPlacementAddresses(n.place.Address()),
		daprd.WithEnableMTLS(true),
		daprd.WithAppPort(n.srv3.Port()),
	)

	return []framework.Option{
		framework.WithProcesses(n.sentry, n.place, n.srv1, n.srv2, n.srv3, n.daprd1, n.daprd2, n.daprd3),
	}
}

func (n *tls) Run(t *testing.T, ctx context.Context) {
	n.sentry.WaitUntilRunning(t, ctx)
	n.place.WaitUntilRunning(t, ctx)
	n.daprd1.WaitUntilRunning(t, ctx)
	n.daprd2.WaitUntilRunning(t, ctx)
	n.daprd3.WaitUntilRunning(t, ctx)

	t.Run("host1 can see actor 1 in ns1, but not actors 2 and 3 in ns2", func(t *testing.T) {
		client := n.daprd1.GRPCClient(t, ctx)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			val1, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor1",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
			assert.Equal(t, "OK1", string(val1.GetData()))

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor2",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.Error(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor3",
				ActorId:   "myactorid",
				Method:    "foo",
			})

			assert.Error(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "inexistant-actor",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.Error(c, err)
		}, time.Second*20, time.Millisecond*10, "actor not ready")
	})

	t.Run("host2 can see actors 1,2,3 in ns2, but not actor 1 in ns1", func(t *testing.T) {
		client := n.daprd2.GRPCClient(t, ctx)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			val2, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor1",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
			assert.Equal(t, "OK3", string(val2.GetData()))

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor2",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor3",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "inexistant-actor",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.Error(c, err)
		}, time.Second*20, time.Millisecond*10, "actors not ready")
	})

	t.Run("host3 can see actors 1,2,3 in ns2, but not actor 1 in ns1", func(t *testing.T) {
		client := n.daprd3.GRPCClient(t, ctx)

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			val3, err := client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor1",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)
			assert.Equal(t, "OK3", string(val3.GetData()))

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor2",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "actor3",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.NoError(c, err)

			_, err = client.InvokeActor(ctx, &rtv1.InvokeActorRequest{
				ActorType: "inexistant-actor",
				ActorId:   "myactorid",
				Method:    "foo",
			})
			assert.Error(c, err)
		}, time.Second*20, time.Millisecond*10, "actors not ready")
	})
}
