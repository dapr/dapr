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
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(resiliencyPubSubDrain))
}

// resiliencyPubSubDrain verifies that a SIGHUP restart waits for a pub/sub
// handler that outlasts the normal graceful shutdown window.
type resiliencyPubSubDrain struct {
	daprd     *daprd.Daprd
	logOut    *log.Log
	resDir    string
	started   chan struct{}
	release   chan struct{}
	finished  chan struct{}
	startOnce sync.Once
}

func (r *resiliencyPubSubDrain) Setup(t *testing.T) []framework.Option {
	r.logOut = log.New()
	r.resDir = t.TempDir()
	r.started = make(chan struct{})
	r.release = make(chan struct{})
	r.finished = make(chan struct{})

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/subscribe", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "[]")
	})
	handler.HandleFunc("/orders", func(w http.ResponseWriter, _ *http.Request) {
		first := false
		r.startOnce.Do(func() {
			first = true
			close(r.started)
		})
		if first {
			<-r.release
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"SUCCESS"}`)
		if first {
			close(r.finished)
		}
	})
	app := prochttp.New(t, prochttp.WithHandler(handler))

	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "pubsub.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: orderpubsub
spec:
  type: pubsub.in-memory
  version: v1
`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "subscription.yaml"), []byte(`
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: orders
spec:
  pubsubname: orderpubsub
  topic: orders
  route: /orders
`), 0o600))
	r.writeResiliency(t, "before")

	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hotreload
spec:
  features:
    - name: HotReload
      enabled: true
`), 0o600))

	r.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithConfigs(configFile),
		daprd.WithResourcesDir(r.resDir),
		daprd.WithDaprGracefulShutdownSeconds(2),
		daprd.WithExecOptions(exec.WithStdout(r.logOut), exec.WithStderr(r.logOut)),
	)

	return []framework.Option{framework.WithProcesses(app, r.daprd)}
}

func (r *resiliencyPubSubDrain) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)
	defer func() {
		select {
		case <-r.release:
		default:
			close(r.release)
		}
	}()

	httpClient := client.HTTP(t)
	publish := func() error {
		reqURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/orderpubsub/orders", r.daprd.HTTPPort())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(`{"number":1}`))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			return fmt.Errorf("publish returned HTTP %d", resp.StatusCode)
		}
		return nil
	}

	published := make(chan error, 1)
	go func() { published <- publish() }()

	select {
	case <-r.started:
	case <-time.After(10 * time.Second):
		t.Fatal("pub/sub handler did not start")
	}

	r.logOut.Reset()
	r.writeResiliency(t, "after")
	require.Eventually(t, func() bool {
		return r.logOut.Contains("Received signal 'hangup'; restarting")
	}, 10*time.Second, 10*time.Millisecond)

	// The normal shutdown limit is two seconds. Keep the handler running
	// beyond it, then let the restart drain finish.
	time.Sleep(3 * time.Second)
	close(r.release)
	select {
	case <-r.finished:
	case <-time.After(10 * time.Second):
		t.Fatal("pub/sub handler did not complete")
	}
	require.NoError(t, <-published)

	r.daprd.WaitUntilRunning(t, ctx)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		policies := r.daprd.GetMetaResiliencies(c, ctx)
		assert.Len(c, policies, 1)
		if len(policies) == 1 {
			assert.Equal(c, "after", policies[0].GetName())
		}
	}, 15*time.Second, 10*time.Millisecond)
	require.NoError(t, publish())
}

func (r *resiliencyPubSubDrain) writeResiliency(t *testing.T, name string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(r.resDir, "resiliency.yaml"), fmt.Appendf(nil, `
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: %s
spec:
  policies:
    timeouts:
      general: 30s
  targets: {}
`, name), 0o600))
}
