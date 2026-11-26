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

package selfhosted

import (
	"context"
	"fmt"
	nethttp "net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorstatestorebindinginflight))
}

// actorstatestorebindinginflight ensures that reloading a bound state store
// while a call of the bound actor type is in flight waits for that call: the
// call can save its state, which goes to the reloaded store, and completes,
// and only then is its actor deactivated. Meanwhile new calls of the bound
// type are held, and the unbound actor type is served.
type actorstatestorebindinginflight struct {
	app     *actors.Actors
	txDB    *sqlite.SQLite
	otherDB *sqlite.SQLite

	// compFile is the hot-reloadable component file of the bound store.
	compFile string

	boundStore string
	boundType  string
	plainType  string

	// started is closed when the blocked method reaches the app, which
	// returns from it once release is closed, after saving state.
	started chan struct{}
	release chan struct{}
	// saveCode is the status code of the state save the blocked method makes.
	saveCode atomic.Int64

	// deactivated are the IDs of the bound type's actors daprd deactivated.
	lock        sync.Mutex
	deactivated []string
}

func (a *actorstatestorebindinginflight) Setup(t *testing.T) []framework.Option {
	a.boundStore = "wallet-transactions"
	a.boundType = "TransactionActor"
	a.plainType = "PlainActor"
	a.started = make(chan struct{})
	a.release = make(chan struct{})

	a.txDB = sqlite.New(t,
		sqlite.WithName(a.boundStore),
		sqlite.WithCreateStateTables(),
	)
	a.otherDB = sqlite.New(t,
		sqlite.WithName(a.boundStore),
		sqlite.WithCreateStateTables(),
	)

	resDir := t.TempDir()
	a.compFile = filepath.Join(resDir, a.boundStore+".yaml")
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.txDB.GetComponent(t)), 0o600))

	a.app = actors.New(t,
		actors.WithActorTypes(a.boundType, a.plainType),
		actors.WithActorTypeHandler(a.boundType, a.handleBound),
		actors.WithActorTypeHandler(a.plainType, func(nethttp.ResponseWriter, *nethttp.Request) {}),
		// The call in flight must not be cancelled by the drain timeout
		// before the test releases it.
		actors.WithDrainOngoingCallTimeout(time.Minute),
		actors.WithDaprdOptions(
			daprd.WithResourcesDir(resDir),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: `+a.boundStore+`
      actorTypes: ["`+a.boundType+`"]
`),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.txDB, a.otherDB, a.app),
	}
}

// handleBound is the app's handler of the bound actor type. Its method
// "block" waits for release, then saves state through daprd and returns.
func (a *actorstatestorebindinginflight) handleBound(w nethttp.ResponseWriter, r *nethttp.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/actors/"+a.boundType+"/")

	if r.Method == nethttp.MethodDelete {
		a.lock.Lock()
		a.deactivated = append(a.deactivated, path)
		a.lock.Unlock()
		return
	}

	if path != "a1/method/block" {
		return
	}

	close(a.started)
	select {
	case <-a.release:
	case <-r.Context().Done():
		w.WriteHeader(nethttp.StatusServiceUnavailable)
		return
	}

	url := fmt.Sprintf("http://%s/v1.0/actors/%s/a1/state", a.app.Daprd().HTTPAddress(), a.boundType)
	req, err := nethttp.NewRequestWithContext(r.Context(), nethttp.MethodPost, url,
		strings.NewReader(`[{"operation":"upsert","request":{"key":"inflight","value":"v1"}}]`))
	if err != nil {
		w.WriteHeader(nethttp.StatusInternalServerError)
		return
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		w.WriteHeader(nethttp.StatusInternalServerError)
		return
	}
	resp.Body.Close()
	a.saveCode.Store(int64(resp.StatusCode))
}

// invoke calls a method of an actor of the bound type through daprd, and
// returns the status code. It does not use t, so it can run off the test
// goroutine.
func (a *actorstatestorebindinginflight) invoke(ctx context.Context, actorID, method string) (int, error) {
	url := fmt.Sprintf("http://%s/v1.0/actors/%s/%s/method/%s", a.app.Daprd().HTTPAddress(), a.boundType, actorID, method)
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := nethttp.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	return resp.StatusCode, resp.Body.Close()
}

func (a *actorstatestorebindinginflight) deactivatedIDs() []string {
	a.lock.Lock()
	defer a.lock.Unlock()
	return append([]string(nil), a.deactivated...)
}

func (a *actorstatestorebindinginflight) Run(t *testing.T, ctx context.Context) {
	a.app.WaitUntilRunning(t, ctx)

	appID := a.app.AppID()

	a.app.InvokeActor(t, ctx, a.boundType, "a1")
	code, body := a.app.SaveActorStateHTTP(t, ctx, a.boundType, "a1", "before", "v1")
	require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)
	require.Equal(t, []string{appID + "||" + a.boundType + "||a1||before"}, a.txDB.StateKeys(t, ctx))

	// A method of the bound type is blocked in the app.
	blocked := make(chan int, 1)
	go func() {
		c, _ := a.invoke(ctx, "a1", "block")
		blocked <- c
	}()
	select {
	case <-a.started:
	case <-ctx.Done():
		require.Fail(t, "the blocked method did not reach the app")
	}

	// The bound store is reloaded against another database.
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.otherDB.GetComponent(t)), 0o600))

	// New calls of the bound type are held once the drain starts.
	var held int
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		held++
		hctx, cancel := context.WithTimeout(ctx, time.Millisecond*500)
		defer cancel()
		_, err := a.invoke(hctx, fmt.Sprintf("held-%d", held), "foo")
		assert.ErrorIs(c, err, context.DeadlineExceeded)
	}, 20*time.Second, 10*time.Millisecond)

	// The unbound type is served during the drain.
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	a.app.InvokeActor(t, pctx, a.plainType, "a1")
	code, body = a.app.SaveActorStateHTTP(t, pctx, a.plainType, "a1", "k", "v1")
	require.Equalf(t, nethttp.StatusNoContent, code, "body: %s", body)

	// The call in flight is still running, and its actor is active.
	select {
	case c := <-blocked:
		require.Failf(t, "the call in flight ended during the drain", "status code %d", c)
	default:
	}
	assert.NotContains(t, a.deactivatedIDs(), "a1")

	// Released, the call saves its state, which goes to the reloaded store,
	// and completes.
	close(a.release)
	select {
	case c := <-blocked:
		assert.Equal(t, nethttp.StatusOK, c)
	case <-ctx.Done():
		require.Fail(t, "the call in flight did not complete")
	}
	assert.Equal(t, int64(nethttp.StatusNoContent), a.saveCode.Load())
	assert.Equal(t, []string{appID + "||" + a.boundType + "||a1||inflight"}, a.otherDB.StateKeys(t, ctx))
	assert.Equal(t, []string{appID + "||" + a.boundType + "||a1||before"}, a.txDB.StateKeys(t, ctx))

	// Its actor is then deactivated, and the bound type is served again.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Contains(c, a.deactivatedIDs(), "a1")
	}, 20*time.Second, 10*time.Millisecond)
	a.app.InvokeActor(t, ctx, a.boundType, "a1")
}
