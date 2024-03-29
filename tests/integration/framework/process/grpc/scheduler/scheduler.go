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

package scheduler

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

// Option is a function that configures the process.
type Option func(*options)

// Scheduler is a wrapper around a grpc.Server that implements the Scheduler API.
type Scheduler struct {
	*procgrpc.GRPC
}

func New(t *testing.T, fopts ...Option) *Scheduler {
	t.Helper()

	s := &Scheduler{}

	opts := options{
		// TODO: add scheduler server funcs here as needed
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.NotNil(t, opts.sentry, "must provide sentry")

	s.GRPC = procgrpc.New(t, append(opts.grpcopts,
		procgrpc.WithServerOption(func(t *testing.T, ctx context.Context) grpc.ServerOption {
			secProv, err := security.New(ctx, security.Options{
				SentryAddress:           "localhost:" + strconv.Itoa(opts.sentry.Port()),
				ControlPlaneTrustDomain: "localhost",
				ControlPlaneNamespace:   "default",
				TrustAnchors:            opts.sentry.CABundle().TrustAnchors,
				AppID:                   "dapr-scheduler",
				MTLSEnabled:             true,
			})
			require.NoError(t, err)

			secProvErr := make(chan error)
			t.Cleanup(func() {
				select {
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for security provider to stop")
				case err = <-secProvErr:
					require.NoError(t, err)
				}
			})
			go func() {
				secProvErr <- secProv.Run(ctx)
			}()

			sec, err := secProv.Handler(ctx)
			require.NoError(t, err)

			return sec.GRPCServerOptionMTLS()
		}),
		procgrpc.WithRegister(func(s *grpc.Server) {
			srv := &server{
				scheduleJobFn: opts.scheduleJobFn,
				deleteJobFn:   opts.deleteJobFn,
				connectHostFn: opts.connectHostFn,
			}

			schedulerv1pb.RegisterSchedulerServer(s, srv)
			if opts.withRegister != nil {
				opts.withRegister(s)
			}
		}))...)

	return s
}

func (s *Scheduler) Cleanup(t *testing.T) {
	s.GRPC.Cleanup(t)
}
