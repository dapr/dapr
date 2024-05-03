/*
Copyright 2024 The Dapr Authors
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

package authz

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(mtls))
}

// mtls tests scheduler with tls enabled.
type mtls struct {
	sentry    *sentry.Sentry
	scheduler *scheduler.Scheduler
}

func (m *mtls) Setup(t *testing.T) []framework.Option {
	m.sentry = sentry.New(t)

	taFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(taFile, m.sentry.CABundle().TrustAnchors, 0o600))

	m.scheduler = scheduler.New(t,
		scheduler.WithEnableTLS(true),
		scheduler.WithSentryAddress(m.sentry.Address()),
		scheduler.WithTrustAnchorsFile(taFile),
	)

	return []framework.Option{
		framework.WithProcesses(m.sentry, m.scheduler),
	}
}

func (m *mtls) Run(t *testing.T, ctx context.Context) {
	m.sentry.WaitUntilRunning(t, ctx)
	m.scheduler.WaitUntilRunning(t, ctx)

	secProv, err := security.New(ctx, security.Options{
		SentryAddress:           m.sentry.Address(),
		ControlPlaneTrustDomain: "localhost",
		ControlPlaneNamespace:   "default",
		TrustAnchors:            m.sentry.CABundle().TrustAnchors,
		AppID:                   "app-1",
		MTLSEnabled:             true,
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(ctx)

	errCh := make(chan error, 1)
	go func() {
		errCh <- secProv.Run(ctx)
	}()
	t.Cleanup(func() { cancel(); require.NoError(t, <-errCh) })

	sec, err := secProv.Handler(ctx)
	require.NoError(t, err)

	schedulerID, err := spiffeid.FromSegments(sec.ControlPlaneTrustDomain(), "ns", "default", "dapr-scheduler")
	require.NoError(t, err)

	host := m.scheduler.Address()

	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), sec.GRPCDialOptionMTLS(schedulerID))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	req := &schedulerv1pb.ScheduleJobRequest{
		Name: "testJob",
		Job: &schedulerv1pb.Job{
			Schedule: ptr.Of("@daily"),
		},
		Metadata: &schedulerv1pb.ScheduleJobMetadata{
			AppId:     "test",
			Namespace: "default",
			Type: &schedulerv1pb.ScheduleJobMetadataType{
				Type: &schedulerv1pb.ScheduleJobMetadataType_Job{
					Job: new(schedulerv1pb.ScheduleTypeJob),
				},
			},
		},
	}

	_, err = client.ScheduleJob(ctx, req)
	require.NoError(t, err)
}
