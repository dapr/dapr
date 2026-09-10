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

package jobs

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(invalidnames))
}

// invalidnames asserts that job names containing characters that are unsafe in
// an etcd key or a callback URL, or that exceed the length bound, are rejected
// by the scheduler validator.
type invalidnames struct {
	daprd     *daprd.Daprd
	scheduler *scheduler.Scheduler
}

func (i *invalidnames) Setup(t *testing.T) []framework.Option {
	i.scheduler = scheduler.New(t)
	srv := app.New(t)
	i.daprd = daprd.New(t,
		daprd.WithSchedulerAddresses(i.scheduler.Address()),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	return []framework.Option{
		framework.WithProcesses(i.scheduler, i.daprd),
	}
}

func (i *invalidnames) Run(t *testing.T, ctx context.Context) {
	i.scheduler.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	client := i.daprd.GRPCClient(t, ctx)

	badNames := map[string]string{
		"slash":       "bad/name",
		"backslash":   "bad\\name",
		"hash":        "bad#name",
		"question":    "bad?name",
		"nul":         "bad\x00name",
		"newline":     "bad\nname",
		"over length": strings.Repeat("a", 600),
	}

	for name, jobName := range badNames {
		t.Run(name, func(t *testing.T) {
			_, err := client.ScheduleJob(ctx, &runtimev1pb.ScheduleJobRequest{
				Job: &runtimev1pb.Job{
					Name:     jobName,
					Schedule: new("@daily"),
				},
			})
			require.Error(t, err)
		})
	}
}
