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
package authz

/*

import (
	"context"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
)

func init() {
	suite.Register(new(incorrectmtls))
}

// incorrectmtls tests scheduler with tls enabled to schedule jobs.
type incorrectmtls struct {
	daprd     *daprd.Daprd
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (i *incorrectmtls) Setup(t *testing.T) []framework.Option {
	i.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, i.sentry.CABundle().TrustAnchors, 0o600))

	i.scheduler = scheduler.New(t,
		scheduler.WithEnableTLS(true),
		scheduler.WithSentryAddress(i.sentry.Address()),
		scheduler.WithTrustAnchorsFile(taFile),
	)

	//TODO: add daprd and scheudle job thru daprd
	i.daprd = daprd.New(t,
		daprd.WithSchedulerAddress(i.scheduler.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(i.daprd, i.sentry, i.scheduler),
		//framework.WithProcesses(i.daprd, i.scheduler),
	}
}

func (i *incorrectmtls) Run(t *testing.T, ctx context.Context) {
	i.sentry.WaitUntilRunning(t, ctx)
	i.scheduler.WaitUntilRunning(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)

	// intentionally insecure
///*i.daprd.GRPCAddress()
	conn, err := grpc.DialContext(ctx, i.scheduler.Address() , grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	//client := rtv1.NewDaprClient(conn)

	t.Run("incorrect mtls", func(t *testing.T) {
		req := &schedulerv1pb.ScheduleJobRequest{
			Job: &rtv1.Job{
				Name:     "test",
				Schedule: "@daily",
				Data: &anypb.Any{
					Value: []byte("test"),
				},
				Repeats: 1,
				DueTime: "0h0m9s0ms",
				Ttl:     "20s",
			}}
		_, err = client.ScheduleJob(ctx, req)
		log.Printf("CASSIE ERROR %v", err)

		require.Error(t, err)
	})
}
*/
//
// func (j *schedulejobs) Run(t *testing.T, ctx context.Context) {
//	i.sentry.WaitUntilRunning(t, ctx)
//	i.scheduler.WaitUntilRunning(t, ctx)
//	i.daprd.WaitUntilRunning(t, ctx)
//
//	secProv, err := security.New(ctx, security.Options{
//		SentryAddress:           i.sentry.Address(),
//		ControlPlaneTrustDomain: "localhost",
//		ControlPlaneNamespace:   "default",
//		TrustAnchors:            i.sentry.CABundle().TrustAnchors,
//		AppID:                   "app-1",
//		MTLSEnabled:             true,
//	})
//	require.NoError(t, err)
//
//	ctx, cancel := context.WithCancel(ctx)
//
//	errCh := make(chan error, 1)
//	go func() {
//		errCh <- secProv.Run(ctx)
//	}()
//	t.Cleanup(func() { cancel(); require.NoError(t, <-errCh) })
//
//	sec, err := secProv.Handler(ctx)
//	require.NoError(t, err)
//
//	schedulerID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", "default", "dapr-scheduler")
//	require.NoError(t, err)
//
//	//host := i.scheduler.Address()
//
//	conn, err := grpc.DialContext(ctx, i.daprd.GRPCAddress(), grpc.WithBlock(), sec.GRPCDialOptionMTLS(schedulerID))
//	require.NoError(t, err)
//	t.Cleanup(func() { require.NoError(t, conn.Close()) })
//
//	//client := schedulerv1pb.NewSchedulerClient(conn)
//	client := runtimev1pb.NewDaprClient(conn)
//
//	for i := 0; i < 10; i++ {
//		req := &runtimev1pb.ScheduleJobRequest{
//			Job: &runtimev1pb.Job{
//				Name:     "test1",
//				Schedule: "@daily",
//				Data: &anypb.Any{
//					Value: []byte("test"),
//				},
//				Repeats: 1,
//				DueTime: "0h0m9s0ms",
//				Ttl:     "20s",
//			}}
//		//req := &schedulerv1pb.ScheduleJobRequest{
//		//	Job: &runtimev1pb.Job{
//		//		Name:     fmt.Sprintf("testJob%d", i),
//		//		Schedule: "@daily",
//		//	},
//		//	Namespace: "default",
//		//	Metadata:  map[string]string{"app_id": "test"},
//		//}
//
//		_, err = client.ScheduleJob(ctx, req)
//		require.NoError(t, err)
//	}
//}
