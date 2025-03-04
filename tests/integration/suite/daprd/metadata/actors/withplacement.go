/*
Copyright 2025 The Dapr Authors
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

package actors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(withplacement))
}

type withplacement struct {
	daprd *daprd.Daprd
}

func (w *withplacement) Setup(t *testing.T) []framework.Option {
	place := placement.New(t)
	w.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(place.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(place, w.daprd),
	}
}

func (w *withplacement) Run(t *testing.T, ctx context.Context) {
	w.daprd.WaitUntilRunning(t, ctx)

	exp := &rtv1.ActorRuntime{
		RuntimeStatus: rtv1.ActorRuntime_RUNNING,
		HostReady:     true,
		Placement:     "placement: connected",
	}

	resp, err := w.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, exp, resp.GetActorRuntime())
	assert.Equal(t, &daprd.MetadataActorRuntime{
		RuntimeStatus: "RUNNING",
		HostReady:     true,
		Placement:     "placement: connected",
	}, w.daprd.GetMetaActorRuntime(t, ctx))
}
