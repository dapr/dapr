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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1 "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(overwrite))
}

type overwrite struct {
	db        *sqlite.SQLite
	app       *app.App
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (o *overwrite) Setup(t *testing.T) []framework.Option {
	o.db = sqlite.New(t, sqlite.WithActorStateStore(true))
	o.app = app.New(t,
		app.WithConfig(`{"entities": ["myactortype"]}`),
		app.WithHandlerFunc("/actors/myactortype/myactorid", func(http.ResponseWriter, *http.Request) {}),
	)
	o.scheduler = scheduler.New(t, scheduler.WithLogLevel("debug"))
	o.place = placement.New(t)

	return []framework.Option{
		framework.WithProcesses(o.db, o.scheduler, o.place, o.app),
	}
}

func (o *overwrite) Run(t *testing.T, ctx context.Context) {
	opts := []daprd.Option{
		daprd.WithResourceFiles(o.db.GetComponent(t)),
		daprd.WithPlacementAddresses(o.place.Address()),
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithAppPort(o.app.Port()),
	}

	optsWithoutScheduler := []daprd.Option{
		daprd.WithResourceFiles(o.db.GetComponent(t)),
		daprd.WithPlacementAddresses(o.place.Address()),
		daprd.WithSchedulerAddresses(o.scheduler.Address()),
		daprd.WithAppPort(o.app.Port()),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: schedulerreminders
spec:
  features:
  - name: SchedulerReminders
    enabled: false
`),
	}

	daprd1 := daprd.New(t, opts...)
	daprd2 := daprd.New(t, optsWithoutScheduler...)
	daprd3 := daprd.New(t, opts...)

	daprd1.Run(t, ctx)
	daprd1.WaitUntilRunning(t, ctx)
	client := daprd1.GRPCClient(t, ctx)
	_, err := client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "10000s",
		Period:    "R100/PT10000S",
		Data:      []byte("mydata1"),
		Ttl:       "10000s",
	})
	require.NoError(t, err)
	sclient := o.scheduler.Client(t, ctx)
	resp, err := sclient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
		Metadata: &schedulerv1.JobMetadata{
			AppId:     daprd1.AppID(),
			Namespace: daprd1.Namespace(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Id:   "myactorid",
						Type: "myactortype",
					},
				},
			},
		},
	})
	require.NoError(t, err)

	require.Len(t, resp.GetJobs(), 1)
	njob := resp.GetJobs()[0]
	assert.Equal(t, "myreminder", njob.GetName())
	expAny, err := anypb.New(wrapperspb.Bytes([]byte(`"bXlkYXRhMQ=="`)))
	require.NoError(t, err)
	assert.Equal(t, &schedulerv1.Job{
		Schedule: ptr.Of("@every 2h46m40s"),
		DueTime:  ptr.Of("10000s"),
		Ttl:      ptr.Of("10000s"),
		Data:     expAny,
		Repeats:  ptr.Of(uint32(100)),
		FailurePolicy: &schedulerv1.FailurePolicy{
			Policy: &schedulerv1.FailurePolicy_Constant{
				Constant: &schedulerv1.FailurePolicyConstant{
					Interval:   durationpb.New(time.Second * 1),
					MaxRetries: ptr.Of(uint32(3)),
				},
			},
		},
	}, njob.GetJob())
	daprd1.Cleanup(t)

	daprd2.Run(t, ctx)
	daprd2.WaitUntilRunning(t, ctx)
	client = daprd2.GRPCClient(t, ctx)
	_, err = client.RegisterActorReminder(ctx, &rtv1.RegisterActorReminderRequest{
		ActorType: "myactortype",
		ActorId:   "myactorid",
		Name:      "myreminder",
		DueTime:   "20000s",
		Period:    "R200/PT20000S",
		Data:      []byte("mydata2"),
		Ttl:       "20000s",
	})
	require.NoError(t, err)
	eresp, err := o.scheduler.ETCDClient(t, ctx).KV.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
	require.NoError(t, err)
	assert.Len(t, eresp.Kvs, 1)

	resp, err = sclient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
		Metadata: &schedulerv1.JobMetadata{
			AppId:     daprd2.AppID(),
			Namespace: daprd2.Namespace(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Id:   "myactorid",
						Type: "myactortype",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetJobs(), 1)
	njob = resp.GetJobs()[0]
	assert.Equal(t, "myreminder", njob.GetName())
	expAny, err = anypb.New(wrapperspb.Bytes([]byte(`"bXlkYXRhMQ=="`)))
	require.NoError(t, err)
	assert.Equal(t, &schedulerv1.Job{
		Schedule: ptr.Of("@every 2h46m40s"),
		DueTime:  ptr.Of("10000s"),
		Ttl:      ptr.Of("10000s"),
		Data:     expAny,
		Repeats:  ptr.Of(uint32(100)),
		FailurePolicy: &schedulerv1.FailurePolicy{
			Policy: &schedulerv1.FailurePolicy_Constant{
				Constant: &schedulerv1.FailurePolicyConstant{
					Interval:   durationpb.New(time.Second * 1),
					MaxRetries: ptr.Of(uint32(3)),
				},
			},
		},
	}, njob.GetJob())
	daprd2.Cleanup(t)

	daprd3.Run(t, ctx)
	daprd3.WaitUntilRunning(t, ctx)
	resp, err = sclient.ListJobs(ctx, &schedulerv1.ListJobsRequest{
		Metadata: &schedulerv1.JobMetadata{
			AppId:     daprd2.AppID(),
			Namespace: daprd2.Namespace(),
			Target: &schedulerv1.JobTargetMetadata{
				Type: &schedulerv1.JobTargetMetadata_Actor{
					Actor: &schedulerv1.TargetActorReminder{
						Id:   "myactorid",
						Type: "myactortype",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.GetJobs(), 1)
	njob = resp.GetJobs()[0]
	assert.Equal(t, "myreminder", njob.GetName())
	expAny, err = anypb.New(wrapperspb.Bytes([]byte(`"bXlkYXRhMg=="`)))
	require.NoError(t, err)
	assert.Equal(t, "@every 5h33m20s", njob.GetJob().GetSchedule())
	assert.Equal(t, "20000s", njob.GetJob().GetDueTime())
	expTTL := time.Now().Add(20000 * time.Second)
	gotTTL, err := time.Parse(time.RFC3339, njob.GetJob().GetTtl())
	require.NoError(t, err)
	assert.InDelta(t, expTTL.UnixMilli(), gotTTL.UnixMilli(), float64(time.Second*10))
	assert.Equal(t, expAny, njob.GetJob().GetData())
	assert.Equal(t, uint32(200), njob.GetJob().GetRepeats())
	daprd3.Cleanup(t)
}
