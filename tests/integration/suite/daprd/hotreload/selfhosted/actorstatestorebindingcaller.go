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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd/actors"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(actorstatestorebindingcaller))
}

// actorstatestorebindingcaller ensures that reloading a bound state store on
// a sidecar which only calls the bound actor type, hosted by another app
// with the same Configuration, leaves that sidecar's calls alone: it has no
// actors of the type to deactivate, so the call in flight is not drained and
// new calls are not held.
type actorstatestorebindingcaller struct {
	host   *actors.Actors
	caller *actors.Actors
	// logline matches the caller's log of the reload.
	logline *logline.LogLine

	txDB    *sqlite.SQLite
	otherDB *sqlite.SQLite

	// compFile is the hot-reloadable component file of the bound store on
	// the caller.
	compFile string

	boundStore string
	boundType  string

	// started is closed when the blocked method reaches the host app, which
	// returns from it once release is closed.
	started chan struct{}
	release chan struct{}

	// deactivated counts the deactivation calls to the host app.
	deactivated atomic.Int64
}

func (a *actorstatestorebindingcaller) Setup(t *testing.T) []framework.Option {
	a.boundStore = "wallet-transactions"
	a.boundType = "TransactionActor"
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

	config := `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: actorstatestores
spec:
  actors:
    stateStores:
    - name: ` + a.boundStore + `
      actorTypes: ["` + a.boundType + `"]
`

	a.host = actors.New(t,
		actors.WithActorTypes(a.boundType),
		actors.WithActorTypeHandler(a.boundType, func(_ nethttp.ResponseWriter, r *nethttp.Request) {
			if r.Method == nethttp.MethodDelete {
				a.deactivated.Add(1)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/a1/method/block") {
				close(a.started)
				select {
				case <-a.release:
				case <-r.Context().Done():
				}
			}
		}),
		actors.WithDaprdOptions(
			daprd.WithResourceFiles(a.txDB.GetComponent(t)),
			daprd.WithConfigManifests(t, config),
		),
	)

	resDir := t.TempDir()
	a.compFile = filepath.Join(resDir, a.boundStore+".yaml")
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.txDB.GetComponent(t)), 0o600))

	a.logline = logline.New(t, logline.WithStdoutLineContains(
		"Bound actor state store "+a.boundStore+" reloaded - this app hosts none of its actor types ["+a.boundType+"]",
	))

	a.caller = actors.New(t,
		actors.WithPeerActor(a.host),
		actors.WithActorTypes("CallerActor"),
		actors.WithActorTypeHandler("CallerActor", func(nethttp.ResponseWriter, *nethttp.Request) {}),
		actors.WithDaprdOptions(
			daprd.WithLogLineStdout(a.logline),
			daprd.WithResourcesDir(resDir),
			daprd.WithConfigManifests(t, config),
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.logline, a.txDB, a.otherDB, a.host, a.caller),
	}
}

// invoke calls a method of an actor of the bound type through the caller's
// sidecar, and returns the status code. It does not use t, so it can run off
// the test goroutine.
func (a *actorstatestorebindingcaller) invoke(ctx context.Context, actorID, method string) (int, error) {
	url := fmt.Sprintf("http://%s/v1.0/actors/%s/%s/method/%s", a.caller.Daprd().HTTPAddress(), a.boundType, actorID, method)
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

func (a *actorstatestorebindingcaller) Run(t *testing.T, ctx context.Context) {
	a.host.WaitUntilRunning(t, ctx)
	a.caller.WaitUntilRunning(t, ctx)

	// The caller reaches the bound type on the host.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		code, err := a.invoke(ctx, "a0", "foo")
		if assert.NoError(c, err) {
			assert.Equal(c, nethttp.StatusOK, code)
		}
	}, 20*time.Second, 10*time.Millisecond)

	// A call the caller makes to the bound type is blocked in the host app.
	blocked := make(chan int, 1)
	go func() {
		code, _ := a.invoke(ctx, "a1", "block")
		blocked <- code
	}()
	select {
	case <-a.started:
	case <-ctx.Done():
		require.Fail(t, "the blocked method did not reach the host app")
	}

	// The bound store is reloaded on the caller only.
	require.NoError(t, os.WriteFile(a.compFile, []byte(a.otherDB.GetComponent(t)), 0o600))
	a.logline.EventuallyFoundAll(t)

	// New calls from the caller are not held.
	nctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	code, err := a.invoke(nctx, "a2", "foo")
	require.NoError(t, err)
	assert.Equal(t, nethttp.StatusOK, code)

	// The call in flight is not drained, and no actor is deactivated.
	select {
	case c := <-blocked:
		require.Failf(t, "the call in flight ended after the reload", "status code %d", c)
	default:
	}
	assert.Equal(t, int64(0), a.deactivated.Load())

	close(a.release)
	select {
	case c := <-blocked:
		assert.Equal(t, nethttp.StatusOK, c)
	case <-ctx.Done():
		require.Fail(t, "the call in flight did not complete")
	}
	assert.Equal(t, int64(0), a.deactivated.Load())
}
