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

package metadata

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(disabled))
}

// disabled tests the response of the metadata API when the actor runtime is disabled
type disabled struct {
	daprd *daprd.Daprd
}

func (m *disabled) Setup(t *testing.T) []framework.Option {
	handler := http.NewServeMux()
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.daprd = daprd.New(t, // "WithPlacementAddress" is missing on purpose, to disable the actor runtime
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithLogLevel("info"), // Daprd is super noisy in debug mode when connecting to placement.
	)

	return []framework.Option{
		framework.WithProcesses(srv, m.daprd),
	}
}

func (m *disabled) Run(t *testing.T, ctx context.Context) {
	// Test an app that has the actor runtime disabled (when there's no placement address)

	m.daprd.WaitUntilTCPReady(t, ctx)

	client := util.HTTPClient(t)

	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		res := getMetadata(t, ctx, client, m.daprd.HTTPPort())
		assert.Equal(t, "DISABLED", res.ActorRuntime.RuntimeStatus)
		assert.False(t, res.ActorRuntime.HostReady)
		assert.Empty(t, res.ActorRuntime.Placement)
		assert.Empty(t, res.ActorRuntime.ActiveActors)
	}, 10*time.Second, 100*time.Millisecond)
}
