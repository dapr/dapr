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

package actorstate

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/iowriter"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(update))
}

type update struct {
	daprd            *daprd.Daprd
	loglineUpdate    *logline.LogLine
	loglineDuplicate *logline.LogLine
	resDir           string
	deactivatedCh    chan string
}

func (a *update) Setup(t *testing.T) []framework.Option {
	a.deactivatedCh = make(chan string, 10)

	handler := chi.NewRouter()
	handler.Get("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"entities": ["myactortype"]}`))
	})
	handler.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler.Delete("/actors/{actorType}/{actorId}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		a.deactivatedCh <- chi.URLParam(r, "actorType") + "/" + chi.URLParam(r, "actorId")
	})
	handler.Put("/actors/{actorType}/{actorId}/method/foo", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`"bar"`))
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	place := placement.New(t)

	a.loglineUpdate = logline.New(t, logline.WithStdoutLineContains(
		"Actor state store mystore updated - actor hosting continues",
	))
	a.loglineDuplicate = logline.New(t, logline.WithStdoutLineContains(
		"mystore is already the actor state store, only one actor state store is allowed",
	))

	a.resDir = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(actorStateStoreComp), 0o600))

	a.daprd = daprd.New(t,
		daprd.WithResourcesDir(a.resDir),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithExecOptions(
			exec.WithStdout(iowriter.NewMultiWriteCloser(
				a.loglineUpdate.Stdout(),
				a.loglineDuplicate.Stdout(),
			)),
		),
	)

	return []framework.Option{
		framework.WithProcesses(srv, place, a.loglineUpdate, a.loglineDuplicate, a.daprd),
	}
}

func (a *update) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	httpClient := client.HTTP(t)

	invokeActor := func(c *assert.CollectT) (int, string) {
		url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactor1/method/foo", a.daprd.HTTPAddress())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		require.NoError(c, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		return resp.StatusCode, string(body)
	}

	actorState := func(c *assert.CollectT, method, path string, body io.Reader) (int, string) {
		url := fmt.Sprintf("http://%s/v1.0/actors/myactortype/myactor1/state%s", a.daprd.HTTPAddress(), path)
		req, err := http.NewRequestWithContext(ctx, method, url, body)
		require.NoError(c, err)
		resp, err := httpClient.Do(req)
		require.NoError(c, err)
		defer resp.Body.Close()
		respBody, err := io.ReadAll(resp.Body)
		require.NoError(c, err)
		return resp.StatusCode, string(respBody)
	}

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		code, body := invokeActor(c)
		assert.Equalf(c, http.StatusOK, code, "body: %s", body)
	}, time.Second*20, time.Millisecond*10)

	t.Run("updating the actor state store in place swaps without draining", func(t *testing.T) {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := actorState(c, http.MethodPost, "",
				strings.NewReader(`[{"operation":"upsert","request":{"key":"mykey","value":"myvalue"}}]`))
			assert.Equalf(c, http.StatusNoContent, code, "body: %s", body)
		}, time.Second*20, time.Millisecond*10)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := actorState(c, http.MethodGet, "/mykey", nil)
			assert.Equal(c, http.StatusOK, code)
			assert.Equal(c, `"myvalue"`, body)
		}, time.Second*10, time.Millisecond*10)

		require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mystore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   value: "true"
 - name: marker
   value: "updated"
`), 0o600))

		// The new (empty) in-memory store instance is picked up per call,
		// proving the store was reloaded.
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, _ := actorState(c, http.MethodGet, "/mykey", nil)
			assert.Equal(c, http.StatusNoContent, code)
		}, time.Second*20, time.Millisecond*10)

		// Wait for the actor runtime to have processed the store change, so
		// asserting no deactivation happened is deterministic: a drain would
		// have been delivered before this log line.
		a.loglineUpdate.EventuallyFoundAll(t)

		// The hosted actor was not deactivated and keeps being invocable.
		select {
		case act := <-a.deactivatedCh:
			assert.Failf(t, "unexpected actor deactivation", "actor %s", act)
		default:
		}
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := invokeActor(c)
			assert.Equalf(c, http.StatusOK, code, "body: %s", body)
		}, time.Second*10, time.Millisecond*10)
	})

	t.Run("a second actor state store component is skipped", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(a.resDir, "state2.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: otherstore
spec:
 type: state.in-memory
 version: v1
 metadata:
 - name: actorStateStore
   value: "true"
`), 0o600))

		a.loglineDuplicate.EventuallyFoundAll(t)

		// The second store is not registered and the original remains the
		// actor state store; actors keep working.
		comps := a.daprd.GetMetaRegisteredComponents(t, ctx)
		names := make([]string, 0, len(comps))
		for _, comp := range comps {
			names = append(names, comp.GetName())
		}
		assert.NotContains(t, names, "otherstore")

		require.EventuallyWithT(t, func(c *assert.CollectT) {
			code, body := invokeActor(c)
			assert.Equalf(c, http.StatusOK, code, "body: %s", body)
		}, time.Second*20, time.Millisecond*10)
	})
}
