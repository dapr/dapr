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
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(one))
}

type one struct {
	scheduler *scheduler.Scheduler
	daprd     *daprd.Daprd
}

func (o *one) Setup(t *testing.T) []framework.Option {
	o.scheduler = scheduler.New(t)
	o.daprd = daprd.New(t,
		daprd.WithScheduler(o.scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(o.scheduler, o.daprd),
	}
}

func (o *one) Run(t *testing.T, ctx context.Context) {
	o.daprd.WaitUntilRunning(t, ctx)

	resp, err := o.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Equal(t, []string{
		o.scheduler.Address(),
	}, resp.GetScheduler().GetConnectedAddresses())

	sched := o.daprd.GetMetaScheduler(t, ctx)
	require.NotNil(t, sched)
	assert.Equal(t, []string{o.scheduler.Address()}, sched.GetConnectedAddresses())
}
