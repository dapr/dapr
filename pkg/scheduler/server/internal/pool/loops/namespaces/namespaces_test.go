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

package namespaces

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/diagridio/go-etcd-cron/api"
	frameworkfake "github.com/diagridio/go-etcd-cron/tests/framework/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	serverfake "github.com/dapr/dapr/pkg/scheduler/server/fake"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/ptr"
)

type suite struct {
	cancelCalled *atomic.Pointer[error]
	connLoop     loop.Interface[loops.Event]
	prefixes     *atomic.Pointer[[]string]
}

type serverclient struct {
	srv          *serverfake.Fake
	clientstream schedulerv1pb.Scheduler_WatchJobsClient
	serverstream schedulerv1pb.Scheduler_WatchJobsServer
	closeserver  context.CancelCauseFunc
	serverclosed *atomic.Bool
}

func newClientServer(t *testing.T) *serverclient {
	t.Helper()

	srv := serverfake.New(t)

	serstreamCh := make(chan schedulerv1pb.Scheduler_WatchJobsServer, 1)
	ctx, cancel := context.WithCancelCause(t.Context())
	var serverclosed atomic.Bool
	srv.WithWatchJobs(func(serstream schedulerv1pb.Scheduler_WatchJobsServer) error {
		serstreamCh <- serstream
		select {
		case <-ctx.Done():
		case <-serstream.Context().Done():
		}
		serverclosed.Store(true)
		return nil
	})

	clstream, err := srv.Client().WatchJobs(t.Context())
	require.NoError(t, err)

	return &serverclient{
		srv:          srv,
		clientstream: clstream,
		serverstream: <-serstreamCh,
		closeserver:  cancel,
		serverclosed: &serverclosed,
	}
}

func newSuite(t *testing.T) *suite {
	t.Helper()

	var cancelCalled atomic.Pointer[error]
	var prefixes atomic.Pointer[[]string]
	prefixes.Store(ptr.Of(make([]string, 0)))
	connLoop := New(Options{
		Cron: frameworkfake.New().WithDeliverablePrefixes(func(_ context.Context, ps ...string) (context.CancelCauseFunc, error) {
			prefixes.Store(ptr.Of(append(*prefixes.Load(), ps...)))
			return func(error) {}, nil
		}),
		CancelPool: func(err error) {
			cancelCalled.Store(&err)
		},
	})

	errCh := make(chan error)
	go func() { errCh <- connLoop.Run(t.Context()) }()
	t.Cleanup(func() {
		select {
		case <-time.After(time.Second * 5):
			require.Fail(t, "timeout")
		case err := <-errCh:
			require.NoError(t, err)
		}
	})

	return &suite{
		connLoop:     connLoop,
		cancelCalled: &cancelCalled,
		prefixes:     &prefixes,
	}
}

func Test_Namespaces(t *testing.T) {
	t.Parallel()

	t.Run("shutdown with no streams", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)
		suite.connLoop.Close(new(loops.Shutdown))
		assert.Nil(t, suite.cancelCalled.Load())
	})

	t.Run("adding and shutting down should see those prefixes added, then those streams closed", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		sc1 := newClientServer(t)
		sc2 := newClientServer(t)
		sc3 := newClientServer(t)
		sc4 := newClientServer(t)

		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc1.serverstream,
			Cancel:  sc1.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: []string{"type1", "type2"},
				AcceptJobTypes: []schedulerv1pb.JobTargetType{
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB,
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
			},
		})
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc2.serverstream,
			Cancel:  sc2.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app2",
				Namespace:  "ns2",
				ActorTypes: []string{"type3", "type4"},
				AcceptJobTypes: []schedulerv1pb.JobTargetType{
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_ACTOR_REMINDER,
				},
			},
		})
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc3.serverstream,
			Cancel:  sc3.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app3",
				Namespace:  "ns3",
				ActorTypes: []string{"type5", "type6"},
				AcceptJobTypes: []schedulerv1pb.JobTargetType{
					schedulerv1pb.JobTargetType_JOB_TARGET_TYPE_JOB,
				},
			},
		})
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc4.serverstream,
			Cancel:  sc4.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app4",
				Namespace:  "ns4",
				ActorTypes: []string{"type7", "type8"},
			},
		})

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.ElementsMatch(c, []string{
				"app||ns1||app1||", "actorreminder||ns1||type1||", "actorreminder||ns1||type2||",
				"actorreminder||ns2||type3||", "actorreminder||ns2||type4||",
				"app||ns3||app3||",
				"app||ns4||app4||", "actorreminder||ns4||type7||", "actorreminder||ns4||type8||",
			}, *suite.prefixes.Load())
		}, time.Second*5, time.Millisecond*10)

		suite.connLoop.Close(new(loops.Shutdown))
		assert.True(t, sc1.serverclosed.Load())
		assert.True(t, sc2.serverclosed.Load())
		assert.True(t, sc3.serverclosed.Load())
		assert.True(t, sc4.serverclosed.Load())
		assert.Nil(t, suite.cancelCalled.Load())
	})

	t.Run("killing the stream should see the connection deleted", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		sc := newClientServer(t)
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc.serverstream,
			Cancel:  sc.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app",
				Namespace:  "ns",
				ActorTypes: []string{"type", "type"},
			},
		})

		require.NoError(t, sc.clientstream.CloseSend())
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.True(c, sc.serverclosed.Load())
		}, time.Second*5, time.Millisecond*10)
		suite.connLoop.Close(new(loops.Shutdown))
	})

	t.Run("multiple triggers are the same time should be sent to connections", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		sc1 := newClientServer(t)
		sc2 := newClientServer(t)
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc1.serverstream,
			Cancel:  sc1.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: []string{"type1", "type2"},
			},
		})
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc2.serverstream,
			Cancel:  sc2.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app2",
				Namespace:  "ns2",
				ActorTypes: []string{"type3", "type4"},
			},
		})

		var resp1 atomic.Pointer[api.TriggerResponseResult]
		var resp2 atomic.Pointer[api.TriggerResponseResult]
		var resp3 atomic.Pointer[api.TriggerResponseResult]
		suite.connLoop.Enqueue(&loops.TriggerRequest{
			ResultFn: func(r api.TriggerResponseResult) {
				resp1.Store(ptr.Of(r))
			},
			Job: &internalsv1pb.JobEvent{
				Key:  "app1||ns1||app1||job1",
				Name: "job1",
				Metadata: &schedulerv1pb.JobMetadata{
					AppId:     "app1",
					Namespace: "ns1",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Job{
							Job: new(schedulerv1pb.TargetJob),
						},
					},
				},
			},
		})
		suite.connLoop.Enqueue(&loops.TriggerRequest{
			ResultFn: func(r api.TriggerResponseResult) {
				resp2.Store(ptr.Of(r))
			},
			Job: &internalsv1pb.JobEvent{
				Key:  "actorreminder||ns1||type1||job1",
				Name: "job1",
				Metadata: &schedulerv1pb.JobMetadata{
					AppId:     "app1",
					Namespace: "ns1",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Id:   "actor1",
								Type: "type1",
							},
						},
					},
				},
			},
		})
		suite.connLoop.Enqueue(&loops.TriggerRequest{
			ResultFn: func(r api.TriggerResponseResult) {
				resp3.Store(ptr.Of(r))
			},
			Job: &internalsv1pb.JobEvent{
				Key:  "actorreminder||ns2||type3||job1",
				Name: "job1",
				Metadata: &schedulerv1pb.JobMetadata{
					AppId:     "app2",
					Namespace: "ns2",
					Target: &schedulerv1pb.JobTargetMetadata{
						Type: &schedulerv1pb.JobTargetMetadata_Actor{
							Actor: &schedulerv1pb.TargetActorReminder{
								Id:   "actor1",
								Type: "type3",
							},
						},
					},
				},
			},
		})

		resp, err := sc1.clientstream.Recv()
		require.NoError(t, err)
		exp := &schedulerv1pb.WatchJobsResponse{
			Name: "job1",
			Id:   1,
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns1",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{
						Job: new(schedulerv1pb.TargetJob),
					},
				},
			},
		}
		assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)

		resp, err = sc1.clientstream.Recv()
		require.NoError(t, err)
		exp = &schedulerv1pb.WatchJobsResponse{
			Name: "job1",
			Id:   2,
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "app1",
				Namespace: "ns1",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id:   "actor1",
							Type: "type1",
						},
					},
				},
			},
		}
		assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)

		resp, err = sc2.clientstream.Recv()
		require.NoError(t, err)
		exp = &schedulerv1pb.WatchJobsResponse{
			Name: "job1",
			Id:   1,
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "app2",
				Namespace: "ns2",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Id:   "actor1",
							Type: "type3",
						},
					},
				},
			},
		}
		assert.True(t, proto.Equal(exp, resp), "%v != %v", exp, resp)

		require.NoError(t, sc2.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     1,
					Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))
		require.NoError(t, sc1.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     2,
					Status: schedulerv1pb.WatchJobsRequestResultStatus_FAILED,
				},
			},
		}))
		require.NoError(t, sc1.clientstream.Send(&schedulerv1pb.WatchJobsRequest{
			WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
				Result: &schedulerv1pb.WatchJobsRequestResult{
					Id:     1,
					Status: schedulerv1pb.WatchJobsRequestResultStatus_SUCCESS,
				},
			},
		}))

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, ptr.Of(api.TriggerResponseResult_SUCCESS), resp1.Load())
			assert.Equal(c, ptr.Of(api.TriggerResponseResult_FAILED), resp2.Load())
			assert.Equal(c, ptr.Of(api.TriggerResponseResult_SUCCESS), resp3.Load())
		}, time.Second*5, time.Millisecond*10)

		suite.connLoop.Close(new(loops.Shutdown))
	})

	t.Run("closing connections during inflights should mark all as undeliverable", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		sc := newClientServer(t)
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc.serverstream,
			Cancel:  sc.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: []string{"type1", "type2"},
			},
		})

		var resp []api.TriggerResponseResult
		var lock sync.Mutex

		for i := range 10 {
			suite.connLoop.Enqueue(&loops.TriggerRequest{
				ResultFn: func(r api.TriggerResponseResult) {
					lock.Lock()
					defer lock.Unlock()
					resp = append(resp, r)
				},
				Job: &internalsv1pb.JobEvent{
					Key:  "app1||ns2||app1||job1",
					Name: "job" + strconv.Itoa(i),
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "app1",
						Namespace: "ns1",
						Target: &schedulerv1pb.JobTargetMetadata{
							Type: &schedulerv1pb.JobTargetMetadata_Job{
								Job: new(schedulerv1pb.TargetJob),
							},
						},
					},
				},
			})
		}
		for range 10 {
			_, err := sc.clientstream.Recv()
			require.NoError(t, err)
		}

		suite.connLoop.Close(new(loops.Shutdown))
		exp := slices.Repeat([]api.TriggerResponseResult{api.TriggerResponseResult_UNDELIVERABLE}, 10)
		assert.Equal(t, exp, resp)
	})

	t.Run("killing streams should mark all inflights as undeliverable", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		sc := newClientServer(t)
		suite.connLoop.Enqueue(&loops.ConnAdd{
			Channel: sc.serverstream,
			Cancel:  sc.closeserver,
			Request: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      "app1",
				Namespace:  "ns1",
				ActorTypes: []string{"type1", "type2"},
			},
		})

		var resp []api.TriggerResponseResult
		var lock sync.Mutex

		for i := range 10 {
			suite.connLoop.Enqueue(&loops.TriggerRequest{
				ResultFn: func(r api.TriggerResponseResult) {
					lock.Lock()
					defer lock.Unlock()
					resp = append(resp, r)
				},
				Job: &internalsv1pb.JobEvent{
					Key:  "app1||ns2||app1||job1",
					Name: "job" + strconv.Itoa(i),
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "app1",
						Namespace: "ns1",
						Target: &schedulerv1pb.JobTargetMetadata{
							Type: &schedulerv1pb.JobTargetMetadata_Job{
								Job: new(schedulerv1pb.TargetJob),
							},
						},
					},
				},
			})
		}

		sc.closeserver(errors.New("closed"))
		suite.connLoop.Close(new(loops.Shutdown))
		exp := slices.Repeat([]api.TriggerResponseResult{api.TriggerResponseResult_UNDELIVERABLE}, 10)
		assert.Equal(t, exp, resp)
	})

	t.Run("adding trigger request which can't be delivered should see them marked as undeliverable ", func(t *testing.T) {
		t.Parallel()

		suite := newSuite(t)

		var resp []api.TriggerResponseResult
		var lock sync.Mutex

		for i := range 10 {
			suite.connLoop.Enqueue(&loops.TriggerRequest{
				ResultFn: func(r api.TriggerResponseResult) {
					lock.Lock()
					defer lock.Unlock()
					resp = append(resp, r)
				},
				Job: &internalsv1pb.JobEvent{
					Key:  "app1||ns2||app1||job1",
					Name: "job" + strconv.Itoa(i),
					Metadata: &schedulerv1pb.JobMetadata{
						AppId:     "app1",
						Namespace: "ns1",
						Target: &schedulerv1pb.JobTargetMetadata{
							Type: &schedulerv1pb.JobTargetMetadata_Job{
								Job: new(schedulerv1pb.TargetJob),
							},
						},
					},
				},
			})
		}

		suite.connLoop.Close(new(loops.Shutdown))
		exp := slices.Repeat([]api.TriggerResponseResult{api.TriggerResponseResult_UNDELIVERABLE}, 10)
		assert.Equal(t, exp, resp)
	})
}
