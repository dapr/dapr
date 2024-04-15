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
	"io"
	"strings"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients     []*client.Client
	namespace   string
	appID       string
	lastUsedIdx int64
}

type Options struct {
	Namespace string
	AppID     string
	Addresses string
	Security  security.Handler
}

func NewManager(ctx context.Context, opts Options) *Manager {
	manager := &Manager{
		namespace: opts.Namespace,
		appID:     opts.AppID,
	}

	schedulerHostPorts := strings.Split(opts.Addresses, ",")

	for _, address := range schedulerHostPorts {
		log.Debugf("Attempting to connect to Scheduler at address: %s", address)
		conn, cli, err := client.New(ctx, address, opts.Security)
		if err != nil {
			log.Debugf("Scheduler client not initialized for address: %s", address)
		} else {
			log.Debugf("Scheduler client initialized for address: %s", address)
		}

		manager.clients = append(manager.clients, &client.Client{
			Conn:      conn,
			Scheduler: cli,
			Address:   address,
			Security:  opts.Security,
		})
	}

	return manager
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	mngr := concurrency.NewRunnerManager()

	// Start a goroutine for each client to watch for job updates
	for _, cli := range m.clients {
		cli := cli
		err := mngr.Add(func(ctx context.Context) error {
			return m.watchJobs(ctx, cli)
		})
		if err != nil {
			return err
		}
	}
	return mngr.Run(ctx)
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
					client.Scheduler = m.NextClient()
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
			stream, err = client.Scheduler.WatchJobs(ctx, streamReq)
			if err != nil {
				log.Errorf("Error while streaming with Scheduler at address %s: %v. Going to close and reconnect.", client.Address, err)
				if err := client.CloseAndReconnect(ctx); err != nil {
					log.Errorf("Error reconnecting Scheduler client at address %s: %v. Trying a new client.", client.Address, err)
					client.Scheduler = m.NextClient()
					continue
				}
				if client.Conn != nil {
					log.Infof("Reconnected to Scheduler at address %s", client.Address)
				}
				continue
			}
		}

		log.Infof("Established stream conn to Scheduler at address %s", client.Address)

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
				if err != nil {
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
				// TODO(Cassie): rm this once it sends the triggered job back to the app
				log.Infof("Received response: %+v %+v", resp.GetData(), resp.GetMetadata())
			}
		}
	}
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
	streamReq := &schedulerv1pb.WatchJobsRequest{
		AppId:     m.appID,
		Namespace: m.namespace,
	}
	return m.establishSchedulerConn(ctx, client, streamReq)
}

// NextClient returns the next client in a round-robin manner.
func (m *Manager) NextClient() schedulerv1pb.SchedulerClient {
	// Check if there is only one client available
	if len(m.clients) == 1 {
		return m.clients[0].Scheduler
	}

	nextIdx := atomic.AddInt64(&m.lastUsedIdx, 1)
	nextClient := m.clients[int(nextIdx)%len(m.clients)]

	return nextClient.Scheduler
}
