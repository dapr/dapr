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
	"math/rand"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	"github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients     []*client.Client
	connDetails scheduler.SidecarConnDetails
	lock        sync.Mutex
	lastUsedIdx int
}

type Options struct {
	Namespace string
	AppID     string
	Addresses string
	Security  security.Handler
}

func NewManager(ctx context.Context, opts Options) *Manager {
	sidecarDetails := scheduler.SidecarConnDetails{
		Namespace: opts.Namespace,
		AppID:     opts.AppID,
	}

	manager := &Manager{
		connDetails: sidecarDetails,
	}

	var schedulerHostPorts []string
	if strings.Contains(opts.Addresses, ",") {
		parts := strings.Split(opts.Addresses, ",")
		for _, part := range parts {
			hostPort := strings.TrimSpace(part)
			schedulerHostPorts = append(schedulerHostPorts, hostPort)
		}
	} else {
		schedulerHostPorts = []string{opts.Addresses}
	}

	for _, address := range schedulerHostPorts {
		log.Debug("Attempting to connect to Scheduler")
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
			if client.Conn != nil {
				if err := client.Conn.Close(); err != nil {
					log.Debugf("error closing Scheduler client connection: %v", err)
				}
			}
			return ctx.Err()
		default:
			if client.Conn != nil {
				// connection is established
				return nil
			} else {
				if err := client.CloseAndReconnect(ctx); err != nil {
					log.Infof("Error establishing conn to Scheduler client: %v. Trying the next client.", err)
					client.Scheduler = m.NextClient()
				}
				log.Infof("Reconnected to scheduler at address %s", client.Address)
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
				log.Infof("Error while streaming with Scheduler: %v. Going to close and reconnect.", err)
				if err := client.CloseAndReconnect(ctx); err != nil {
					log.Debugf("Error reconnecting client: %v. Trying a new client.", err)
					client.Scheduler = m.NextClient()
					continue
				}
				log.Infof("Reconnected to scheduler at address %s", client.Address)
				continue
			}
		}

		log.Infof("Established stream conn to Scheduler at address %s", client.Address)

		for {
			select {
			case <-stream.Context().Done():
				if err := stream.CloseSend(); err != nil {
					log.Debugf("Error closing stream")
				}
				if err := client.CloseConnection(); err != nil {
					log.Debugf("Error closing connection: %v", err)
				}
				return stream.Context().Err()
			case <-ctx.Done():
				if err := stream.CloseSend(); err != nil {
					log.Debugf("Error closing stream")
				}
				if err := client.CloseConnection(); err != nil {
					log.Debugf("Error closing connection: %v", err)
				}
				return ctx.Err()
			default:
				resp, streamerr := stream.Recv()
				if streamerr != nil {
					switch status.Code(streamerr) {
					case codes.Canceled:
						log.Debugf("Sidecar cancelled the Scheduler stream ctx.")
					case codes.Unavailable:
						log.Debugf("Scheduler cancelled the Scheduler stream ctx.")
					default:
						if streamerr == io.EOF {
							log.Debugf("Scheduler cancelled the Sidecar stream ctx.")
						}
						log.Infof("Error while receiving job trigger: %v", streamerr)
						if err := stream.CloseSend(); err != nil {
							log.Debugf("Error closing stream")
						}
						if nerr := client.CloseConnection(); nerr != nil {
							log.Debugf("Error closing connection: %v", nerr)
						}
						return streamerr
					}
				}
				log.Infof("Received response: %+v", resp) // TODO rm this once it sends the triggered job back to the app
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
				log.Debugf("Error establishing connection: %v", err)
				continue
			}
			err = m.processStream(ctx, client, streamReq)
			if err != nil {
				log.Debugf("Error processing stream: %v", err)
				continue
			}
		}
	}
}

// watchJobs starts watching for job triggers from a single scheduler client.
func (m *Manager) watchJobs(ctx context.Context, client *client.Client) error {
	streamReq := &schedulerv1pb.WatchJobsRequest{
		AppId:     m.connDetails.AppID,
		Namespace: m.connDetails.Namespace,
	}
	return m.establishSchedulerConn(ctx, client, streamReq)
}

// NextClient returns the next client in a round-robin manner.
func (m *Manager) NextClient() schedulerv1pb.SchedulerClient {
	m.lock.Lock()
	defer m.lock.Unlock()

	// Check if there is only one client available
	if len(m.clients) == 1 {
		return m.clients[0].Scheduler
	}

	nextIdx := rand.Intn(len(m.clients))
	nextClient := m.clients[nextIdx]

	// Update the last used index
	m.lastUsedIdx = nextIdx

	return nextClient.Scheduler
}
