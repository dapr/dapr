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

package jobs

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(noscheduler))
}

// noscheduler tests daprd invoking jobs API after a bad init.
type noscheduler struct {
	daprd *daprd.Daprd
}

func (b *noscheduler) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t)

	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *noscheduler) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)

	_, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)

	req := &rtv1.ScheduleJobRequest{
		Job: &rtv1.Job{
			Name:     uuid.NewString(),
			Schedule: ptr.Of("@every 20s"),
			Data: &anypb.Any{
				Value: []byte(uuid.NewString()),
			},
		},
	}
	_, err = client.ScheduleJobAlpha1(ctx, req)
	require.Error(t, err)

	_, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
}
