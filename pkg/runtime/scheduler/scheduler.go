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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/golang/protobuf/ptypes/wrappers"

	"github.com/dapr/dapr/pkg/actors"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/scheduler/clients"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

type Options struct {
	Namespace  string
	AppID      string
	Clients    *clients.Clients
	Resiliency resiliency.Provider
	Channels   *channels.Channels
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients    *clients.Clients
	namespace  string
	appID      string
	actors     actors.ActorRuntime
	resiliency resiliency.Provider
	channels   *channels.Channels

	startCh chan struct{}
	started atomic.Bool
	wg      sync.WaitGroup
	jobCh   chan *schedulerv1pb.WatchJobsResponse
	closeCh chan struct{}
	running atomic.Bool
}

func New(opts Options) (*Manager, error) {
	return &Manager{
		namespace:  opts.Namespace,
		appID:      opts.AppID,
		clients:    opts.Clients,
		resiliency: opts.Resiliency,
		channels:   opts.Channels,
		jobCh:      make(chan *schedulerv1pb.WatchJobsResponse),
		startCh:    make(chan struct{}),
		closeCh:    make(chan struct{}),
	}, nil
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	if !m.running.CompareAndSwap(false, true) {
		return errors.New("scheduler manager is already running")
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.startCh:
	}

	clients := m.clients.All()
	runners := make([]concurrency.Runner, len(clients), len(clients)+2)
	for i := range clients {
		i := i
		runners[i] = func(ctx context.Context) error {
			return m.watchJobs(ctx, clients[i])
		}
	}

	runners = append(runners,
		m.processTriggers,
		func(ctx context.Context) error {
			<-ctx.Done()
			close(m.closeCh)
			return nil
		},
	)

	defer m.wg.Wait()
	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (m *Manager) Start(actors actors.ActorRuntime) {
	if m.started.CompareAndSwap(false, true) {
		m.actors = actors
		close(m.startCh)
	}
}

func (m *Manager) establishConnection(ctx context.Context, client *client.Client) error {
	for {
		select {
		case <-ctx.Done():
			var err error
			if client.Conn != nil {
				if err = client.Conn.Close(); err != nil {
					log.Errorf("error closing Scheduler client connection: %v", err)
				}
			}
			return err
		default:
			if client.Conn != nil {
				// connection is established
				log.Debugf("Connection established with Scheduler at address %s", client.Address)
				return nil
			} else {
				if err := client.CloseAndReconnect(ctx); err != nil {
					log.Errorf("Error establishing conn to Scheduler client at address %s: %v. Trying the next client.", client.Address, err)
					client.Scheduler = m.clients.Next()
				}
			}
		}
	}
}

func (m *Manager) processStream(ctx context.Context, client *client.Client, streamReq *schedulerv1pb.WatchJobsRequest) error {
	var stream schedulerv1pb.Scheduler_WatchJobsClient

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var err error
			stream, err = client.Scheduler.WatchJobs(ctx)
			if err != nil {
				log.Errorf("Error while streaming with Scheduler at address %s: %v. Going to close and reconnect.", client.Address, err)
				if err := client.CloseAndReconnect(ctx); err != nil {
					log.Errorf("Error reconnecting Scheduler client at address %s: %v. Trying a new client.", client.Address, err)
					client.Scheduler = m.clients.Next()
					continue
				}
				if client.Conn != nil {
					log.Infof("Reconnected to Scheduler at address %s", client.Address)
				}
				continue
			}
			if err := stream.Send(streamReq); err != nil {
				log.Errorf("Error sending stream request to Scheduler at address %s: %v", client.Address, err)
				continue
			}
		}
		log.Infof("Established stream conn to Scheduler at address %s", client.Address)

		if err := m.handleStream(ctx, client, stream); err != nil {
			return err
		}
	}
}

func (m *Manager) handleStream(ctx context.Context, client *client.Client, stream schedulerv1pb.Scheduler_WatchJobsClient) error {
	var wg sync.WaitGroup
	wg.Add(1)

	respCh := make(chan uint32)

	go func() {
		defer wg.Done()
		for {
			select {
			case <-stream.Context().Done():
			case <-ctx.Done():
			case uuid := <-respCh:
				if err := stream.Send(&schedulerv1pb.WatchJobsRequest{
					WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Result{
						Result: &schedulerv1pb.WatchJobsRequestResult{
							Uuid: uuid,
						},
					},
				}); err != nil {
					log.Errorf("Error sending response to Scheduler at address %s: %v", client.Address, err)
				}
			}
		}
	}()

	for {
		select {
		case <-stream.Context().Done():
			if err := stream.CloseSend(); err != nil {
				log.Errorf("Error closing stream for Scheduler address %s: %v", client.Address, err)
			}
			if err := client.CloseConnection(); err != nil {
				log.Errorf("Error closing conn for Scheduler address %s: %v", client.Address, err)
			}
			return stream.Context().Err()
		case <-ctx.Done():
			if err := stream.CloseSend(); err != nil {
				log.Errorf("Error closing stream for Scheduler address %s", client.Address)
			}
			if err := client.CloseConnection(); err != nil {
				log.Errorf("Error closing connection for Scheduler address %s: %v", client.Address, err)
			}
			return ctx.Err()
		default:
			resp, err := stream.Recv()
			if err == nil {
				m.wg.Add(1)
				go func() {
					defer m.wg.Done()
					select {
					case <-ctx.Done():
					case m.jobCh <- resp:
					}
				}()

				continue
			}

			switch status.Code(err) {
			case codes.Canceled:
				log.Errorf("Sidecar cancelled the stream ctx for Scheduler address %s.", client.Address)
			case codes.Unavailable:
				log.Errorf("Scheduler cancelled the stream ctx for address %s.", client.Address)
			default:
				if err == io.EOF {
					log.Errorf("Scheduler cancelled the Sidecar stream ctx for Scheduler address %s.", client.Address)
				} else {
					log.Errorf("Error while receiving job trigger from Scheduler at address %s: %v", client.Address, err)
				}

				if nerr := stream.CloseSend(); nerr != nil {
					log.Errorf("Error closing stream for Scheduler address: %s", client.Address)
				}
				if nerr := client.CloseConnection(); nerr != nil {
					log.Errorf("Error closing conn for Scheduler address %s: %v", client.Address, nerr)
				}
				return err
			}
		}
	}
}

func (m *Manager) processTriggers(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job := <-m.jobCh:
			log.Debugf("Received scheduled job: %s", job.GetName())
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				m.handleJob(ctx, job)
				// Send Ack to Stream
			}()
		}
	}
}

func (m *Manager) handleJob(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) {
	meta := job.GetMetadata()

	switch t := meta.GetType(); t.GetType().(type) {
	case *schedulerv1pb.ScheduleJobMetadataType_Job:
		if err := m.invokeApp(ctx, job); err != nil {
			log.Errorf("failed to invoke schedule app job: %s", err)
		}

	case *schedulerv1pb.ScheduleJobMetadataType_Actor:
		if err := m.invokeActorReminder(ctx, job); err != nil {
			log.Errorf("failed to invoke scheduled actor reminder: %s", err)
		}

	default:
		log.Errorf("Unknown job metadata type: %+v", t)
	}
}

func (m *Manager) invokeApp(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	appChannel := m.channels.AppChannel()
	if appChannel == nil {
		return errors.New("received app job trigger but app channel not initialized")
	}

	req := invokev1.NewInvokeMethodRequest("job/"+job.GetName()).
		WithHTTPExtension(http.MethodPost, "").
		WithDataObject(job.GetData())
	defer req.Close()

	response, err := appChannel.TriggerJob(ctx, req)
	if err != nil {
		return err
	}

	if response != nil {
		defer response.Close()
	}
	if err != nil {
		// TODO(Cassie): add an orphaned job go routine to retry sending job at a later time
		return fmt.Errorf("error returned from app channel while sending triggered job to app: %w", err)
	}

	statusCode := response.Status().GetCode()
	switch codes.Code(statusCode) {
	case codes.OK:
		log.Debugf("Sent job: %s to app: %s", job.GetName(), m.appID)
	case codes.NotFound:
		log.Errorf("non-retriable error returned from app while processing triggered job %v: %s. status code returned: %v", job.GetName(), statusCode)
	default:
		log.Errorf("unexpected status code returned from app while processing triggered job %s. status code returned: %v", job.GetName(), statusCode)
	}

	return nil
}

func (m *Manager) invokeActorReminder(ctx context.Context, job *schedulerv1pb.WatchJobsResponse) error {
	if m.actors == nil {
		return errors.New("received actor reminder but actor runtime is not initialized")
	}

	actor := job.GetMetadata().GetType().GetActor()

	var jspb wrappers.BytesValue
	if job.GetData() != nil {
		if err := job.GetData().UnmarshalTo(&jspb); err != nil {
			return fmt.Errorf("failed to unmarshal reminder data: %s", err)
		}
	}

	data, err := json.Marshal(&actors.ReminderResponse{
		Data: jspb.GetValue(),
	})
	if err != nil {
		return err
	}

	req := internalv1pb.NewInternalInvokeRequest("remind/"+job.GetName()).
		WithActor(actor.GetType(), actor.GetId()).
		WithData(data).
		WithContentType(internalv1pb.JSONContentType)
	if _, err := m.actors.Call(ctx, req); err != nil {
		return err
	}

	return nil
}

func (m *Manager) establishSchedulerConn(ctx context.Context, client *client.Client, streamReq *schedulerv1pb.WatchJobsRequest) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Keep trying to establish connection and process stream
			err := m.establishConnection(ctx, client)
			if err != nil {
				log.Errorf("Error establishing conn for Scheduler address %s: %v", client.Address, err)
				continue
			}
			err = m.processStream(ctx, client, streamReq)
			if err != nil {
				log.Errorf("Error processing stream for Scheduler address %s: %v", client.Address, err)
				continue
			}
		}
	}
}

// watchJobs starts watching for job triggers from a single scheduler client.
func (m *Manager) watchJobs(ctx context.Context, client *client.Client) error {
	var entities []string
	if m.actors != nil {
		entities = m.actors.Entities()
	}

	streamReq := &schedulerv1pb.WatchJobsRequest{
		WatchJobRequestType: &schedulerv1pb.WatchJobsRequest_Initial{
			Initial: &schedulerv1pb.WatchJobsRequestInitial{
				AppId:      m.appID,
				Namespace:  m.namespace,
				ActorTypes: entities,
			},
		},
	}
	return m.establishSchedulerConn(ctx, client, streamReq)
}
