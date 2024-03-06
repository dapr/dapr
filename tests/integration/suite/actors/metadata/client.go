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
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(client))
}

// client tests the response of the metadata API for a healthy actor client.
type client struct {
	daprd       *daprd.Daprd
	place       *placement.Placement
	blockConfig chan struct{}
}

func (m *client) Setup(t *testing.T) []framework.Option {
	m.blockConfig = make(chan struct{})

	handler := http.NewServeMux()
	handler.HandleFunc("/dapr/config", func(w http.ResponseWriter, r *http.Request) {
		<-m.blockConfig
		w.Write([]byte(`{"entities": []}`))
	})
	handler.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := prochttp.New(t, prochttp.WithHandler(handler))
	m.place = placement.New(t)
	m.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(m.place.Address()),
		daprd.WithAppProtocol("http"),
		daprd.WithAppPort(srv.Port()),
		daprd.WithLogLevel("info"), // Daprd is super noisy in debug mode when connecting to placement.
	)

	return []framework.Option{
		framework.WithProcesses(m.place, srv, m.daprd),
	}
}

func (m *client) Run(t *testing.T, ctx context.Context) {
	// Test an app that is an actor client (no actor state store configured)
	// 1. Assert that status is "INITIALIZING" before /dapr/config is called
	// 2. After init is done, status is "RUNNING", hostReady is "false", placement reports a connection, and hosted actors are empty

	m.place.WaitUntilRunning(t, ctx)
	m.daprd.WaitUntilTCPReady(t, ctx)

	client := util.HTTPClient(t)

	// Before initialization
	res := getMetadata(t, ctx, client, m.daprd.HTTPPort())
	require.False(t, t.Failed())
	assert.Equal(t, "INITIALIZING", res.ActorRuntime.RuntimeStatus)
	assert.False(t, res.ActorRuntime.HostReady)
	assert.Empty(t, res.ActorRuntime.Placement)
	assert.Empty(t, res.ActorRuntime.ActiveActors)

	// Complete init
	close(m.blockConfig)
	assert.EventuallyWithT(t, func(t *assert.CollectT) {
		res := getMetadata(t, ctx, client, m.daprd.HTTPPort())
		assert.Equal(t, "RUNNING", res.ActorRuntime.RuntimeStatus)
		assert.False(t, res.ActorRuntime.HostReady)
		assert.Equal(t, "placement: connected", res.ActorRuntime.Placement)
		assert.Empty(t, res.ActorRuntime.ActiveActors, 0)
	}, 10*time.Second, 100*time.Millisecond)
}
