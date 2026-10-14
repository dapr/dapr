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

package placement

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(authz))
}

// authz asserts report streams are authorized against the caller's SPIFFE
// identity under mTLS: a report whose app ID or namespace does not match the
// identity is refused, as is one claiming another app's internal actor
// types, while a matching report is served.
type authz struct {
	sentry *sentry.Sentry
	sched  *scheduler.Scheduler
}

func (a *authz) Setup(t *testing.T) []framework.Option {
	a.sentry = sentry.New(t)
	a.sched = scheduler.New(t,
		scheduler.WithSentry(a.sentry),
		scheduler.WithID("dapr-scheduler-server-0"),
		scheduler.WithPlacementEnabled(true),
	)
	return []framework.Option{
		framework.WithProcesses(a.sentry, a.sched),
	}
}

func (a *authz) Run(t *testing.T, ctx context.Context) {
	a.sched.WaitUntilRunning(t, ctx)

	stream, err := a.sched.ClientMTLS(t, ctx, "myapp").WatchJobs(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{Initial: &schedulerv1pb.WatchJobsRequestInitial{
			AppId:                      "myapp",
			Namespace:                  "default",
			SupportsSchedulerPlacement: true,
		}},
	}))

	client := a.sched.ClientMTLS(t, ctx, "myapp")
	report := func(host *schedulerv1pb.ActorHost) error {
		rstream, rerr := client.ReportActorTypes(ctx)
		if rerr != nil {
			return rerr
		}
		if rerr = rstream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: host},
		}); rerr != nil && !errors.Is(rerr, io.EOF) {
			return rerr
		}
		_, rerr = rstream.Recv()
		return rerr
	}

	// A matching report is served.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, report(&schedulerv1pb.ActorHost{
			Address:    "127.0.0.1:40001",
			AppId:      "myapp",
			Namespace:  "default",
			ActorTypes: []string{"mytype"},
		}))
	}, time.Second*20, time.Millisecond*100)

	// An app ID not matching the SPIFFE identity is refused.
	err = report(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40002",
		AppId:      "other-app",
		Namespace:  "default",
		ActorTypes: []string{"mytype"},
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// A namespace not matching the SPIFFE identity is refused.
	err = report(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40003",
		AppId:      "myapp",
		Namespace:  "other-ns",
		ActorTypes: []string{"mytype"},
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Claiming another app's internal actor type is refused.
	err = report(&schedulerv1pb.ActorHost{
		Address:    "127.0.0.1:40004",
		AppId:      "myapp",
		Namespace:  "default",
		ActorTypes: []string{"dapr.internal.default.other-app.workflow"},
	})
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}
