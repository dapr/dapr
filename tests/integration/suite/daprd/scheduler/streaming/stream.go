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

package streaming

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(streaming))
}

type streaming struct {
	daprdA     *daprd.Daprd
	daprdB     *daprd.Daprd
	schedulers []*scheduler.Scheduler

	streamloglineDaprA *logline.LogLine
	streamloglineDaprB *logline.LogLine
}

func (s *streaming) Setup(t *testing.T) []framework.Option {
	s.streamloglineDaprA = logline.New(t,
		logline.WithStdoutLineContains(
			`Received response: [type.googleapis.com/google.type.Expr]:{} map[appID:A namespace:A]`,
		),
	)

	s.streamloglineDaprB = logline.New(t,
		logline.WithStdoutLineContains(
			`Received response: [type.googleapis.com/google.type.Expr]:{} map[appID:B namespace:B]`,
		),
	)

	fp := ports.Reserve(t, 6)
	port1, port2, port3 := fp.Port(t), fp.Port(t), fp.Port(t)

	opts := []scheduler.Option{
		// TODO(Cassie): clean up having to do the string templating here. mv it to the test framework.
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler0=http://localhost:%d,scheduler1=http://localhost:%d,scheduler2=http://localhost:%d", port1, port2, port3)),
		scheduler.WithInitialClusterPorts(port1, port2, port3),
	}

	clientPorts := []string{
		"scheduler0=" + strconv.Itoa(fp.Port(t)),
		"scheduler1=" + strconv.Itoa(fp.Port(t)),
		"scheduler2=" + strconv.Itoa(fp.Port(t)),
	}
	s.schedulers = []*scheduler.Scheduler{
		scheduler.New(t, append(opts, scheduler.WithID("scheduler0"), scheduler.WithEtcdClientPorts(clientPorts))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler1"), scheduler.WithEtcdClientPorts(clientPorts))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler2"), scheduler.WithEtcdClientPorts(clientPorts))...),
	}

	s.daprdA = daprd.New(t,
		// TODO(Cassie): rm appID + ns here and log line once streaming to the proper app is tested
		daprd.WithAppID("A"),
		daprd.WithNamespace("A"),
		daprd.WithSchedulerAddresses(s.schedulers[0].Address(), s.schedulers[1].Address(), s.schedulers[2].Address()),
		daprd.WithExecOptions(
			exec.WithStdout(s.streamloglineDaprA.Stdout()),
		),
	)

	s.daprdB = daprd.New(t,
		// TODO(Cassie): rm appID + ns here and log line once streaming to the proper app is tested
		daprd.WithAppID("B"),
		daprd.WithNamespace("B"),
		daprd.WithSchedulerAddresses(s.schedulers[0].Address(), s.schedulers[1].Address(), s.schedulers[2].Address()),
		daprd.WithExecOptions(
			exec.WithStdout(s.streamloglineDaprB.Stdout()),
		),
	)

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(s.streamloglineDaprA, s.streamloglineDaprB,
			s.daprdA, s.daprdB,
			s.schedulers[0], s.schedulers[1], s.schedulers[2],
		),
	}
}

func (s *streaming) Run(t *testing.T, ctx context.Context) {
	s.schedulers[0].WaitUntilRunning(t, ctx)
	s.schedulers[1].WaitUntilRunning(t, ctx)
	s.schedulers[2].WaitUntilRunning(t, ctx)

	s.daprdA.WaitUntilRunning(t, ctx)
	s.daprdB.WaitUntilRunning(t, ctx)

	t.Run("daprA receive its scheduled job on stream at trigger time", func(t *testing.T) {
		daprAclient := s.daprdA.GRPCClient(t, ctx)

		req := &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     "test",
				Schedule: "@every 1s",
				Repeats:  1,
				Data: &anypb.Any{
					TypeUrl: "type.googleapis.com/google.type.Expr",
				},
			},
		}

		_, err := daprAclient.ScheduleJob(ctx, req)
		require.NoError(t, err)

		s.streamloglineDaprA.EventuallyFoundAll(t)
	})

	t.Run("daprB receive its scheduled job on stream at trigger time", func(t *testing.T) {
		daprBclient := s.daprdB.GRPCClient(t, ctx)

		req := &runtimev1pb.ScheduleJobRequest{
			Job: &runtimev1pb.Job{
				Name:     "test",
				Schedule: "@every 1s",
				Repeats:  1,
				Data: &anypb.Any{
					TypeUrl: "type.googleapis.com/google.type.Expr",
				},
			},
		}

		_, err := daprBclient.ScheduleJob(ctx, req)
		require.NoError(t, err)

		s.streamloglineDaprB.EventuallyFoundAll(t)
	})
}
