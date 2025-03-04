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

package scheduler

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(none))
}

type none struct {
	daprd *daprd.Daprd
}

func (n *none) Setup(t *testing.T) []framework.Option {
	n.daprd = daprd.New(t)

	return []framework.Option{
		framework.WithProcesses(n.daprd),
	}
}

func (n *none) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	resp, err := n.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	//nolint:protogetter
	assert.Nil(t, resp.Scheduler)
	assert.Nil(t, n.daprd.GetMetaScheduler(t, ctx))
}
