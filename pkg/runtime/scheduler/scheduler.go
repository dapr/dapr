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
	"fmt"
	"io"
	"math/rand"
	"strings"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	schedulerclient "github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Client represents a client for interacting with the scheduler.
type schedulerClient struct {
	conn      *grpc.ClientConn
	scheduler schedulerv1pb.SchedulerClient
	address   string
	security  security.Handler
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	clients     []*schedulerClient
	connDetails scheduler.SidecarConnDetails
	lock        sync.Mutex
	lastUsedIdx int
}

func NewManager(ctx context.Context, sidecarDetails scheduler.SidecarConnDetails, addresses string, sec security.Handler) *Manager {
	manager := &Manager{
		connDetails: sidecarDetails,
	}

	var schedulerHostPorts []string
	if strings.Contains(addresses, ",") {
		parts := strings.Split(addresses, ",")
		for _, part := range parts {
			hostPort := strings.TrimSpace(part)
			schedulerHostPorts = append(schedulerHostPorts, hostPort)
		}
	} else {
		schedulerHostPorts = []string{addresses}
	}

	for _, address := range schedulerHostPorts {
		log.Debug("Attempting to connect to Scheduler")
		conn, client, err := schedulerclient.New(ctx, address, sec)
		if err != nil {
			log.Debugf("Scheduler client not initialized for address: %s", address)
		} else {
			log.Debugf("Scheduler client initialized for address: %s", address)
		}

		manager.clients = append(manager.clients, &schedulerClient{
			conn:      conn,
			scheduler: client,
			address:   address,
			security:  sec,
		})
	}

	return manager
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) error {
	mngr := concurrency.NewRunnerManager()

	// Start a goroutine for each client to watch for job updates
	for _, client := range m.clients {
		client := client
		err := mngr.Add(func(ctx context.Context) error {
			return m.watchJob(ctx, client)
		})
		if err != nil {
			return err
		}
	}
	return mngr.Run(ctx)
}

func (m *Manager) establishConnection(ctx context.Context, client *schedulerClient) error {
	for {
		select {
		case <-ctx.Done():
			if client.conn != nil {
				if err := client.conn.Close(); err != nil {
					log.Debugf("error closing Scheduler client connection: %v", err)
				}
			}
			return ctx.Err()
		default:
			if client.conn != nil {
				// connection is established
				return nil
			} else {
				if err := client.closeAndReconnect(ctx); err != nil {
					log.Infof("Error establishing conn to Scheduler client: %v. Trying the next client.", err)
					client.scheduler = m.NextClient()
				}
			}
		}
	}
}

func (m *Manager) processStream(ctx context.Context, client *schedulerClient, streamReq *schedulerv1pb.StreamJobRequest) error {
	var stream schedulerv1pb.Scheduler_WatchJobClient

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var err error
			stream, err = client.scheduler.WatchJob(ctx, streamReq)
			if err != nil {
				log.Infof("Error while streaming with Scheduler: %v", err)
				if err := client.closeAndReconnect(ctx); err != nil {
					log.Debugf("Error reconnecting client: %v. Trying a new client.", err)
					client.scheduler = m.NextClient()
					continue
				}
				continue
			}

		}

		log.Infof("Established stream conn to Scheduler at address %s", client.address)

		for {
			select {
			case <-stream.Context().Done():
				if err := stream.CloseSend(); err != nil {
					log.Debugf("Error closing stream")
				}
				if err := client.closeConnection(); err != nil {
					log.Debugf("Error closing connection: %v", err)
				}
				return stream.Context().Err()
			case <-ctx.Done():
				if err := stream.CloseSend(); err != nil {
					log.Debugf("Error closing stream")
				}
				if err := client.closeConnection(); err != nil {
					log.Debugf("Error closing connection: %v", err)
				}
				return ctx.Err()
			default:
				resp, err := stream.Recv()
				if err != nil {
					switch status.Code(err) {
					case codes.Canceled:
						log.Debugf("Sidecar cancelled the Scheduler stream ctx.")
					case codes.Unavailable:
						log.Debugf("Scheduler cancelled the Scheduler stream ctx.")
					default:
						if err == io.EOF {
							log.Debugf("Scheduler cancelled the Sidecar stream ctx.")
						}
						log.Infof("Error while receiving job trigger: %v", err)
						if err := stream.CloseSend(); err != nil {
							log.Debugf("Error closing stream")
						}
						if err := client.closeConnection(); err != nil {
							log.Debugf("Error closing connection: %v", err)
						}
						return err
					}
				}
				log.Infof("Received response: %+v", resp) // TODO rm this once it sends the triggered job back to the app
			}
		}
	}
}

func (m *Manager) establishSchedulerConn(ctx context.Context, client *schedulerClient, streamReq *schedulerv1pb.StreamJobRequest) error {
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

// watchJob starts watching for job triggers from a single scheduler client.
func (m *Manager) watchJob(ctx context.Context, client *schedulerClient) error {
	streamReq := &schedulerv1pb.StreamJobRequest{
		AppId:     m.connDetails.AppID,
		Namespace: m.connDetails.Namespace,
	}
	return m.establishSchedulerConn(ctx, client, streamReq)
}

func (client *schedulerClient) closeConnection() error {
	if client.conn == nil {
		return nil // Connection is already closed
	}

	if err := client.conn.Close(); err != nil {
		return fmt.Errorf("error closing connection: %v", err)
	}

	client.conn = nil // Set the connection to nil to indicate it's closed
	return nil
}

// closeAndReconnect closes the connection and reconnects the client.
func (client *schedulerClient) closeAndReconnect(ctx context.Context) error {
	if client.conn != nil {
		if err := client.conn.Close(); err != nil {
			return fmt.Errorf("error closing connection: %v", err)
		}
	}
	log.Info("Attempting to connect to Scheduler")
	conn, schedulerClient, err := schedulerclient.New(ctx, client.address, client.security)
	if err != nil {
		return fmt.Errorf("error creating Scheduler client for address %s: %v", client.address, err)
	}
	client.conn = conn
	client.scheduler = schedulerClient

	log.Infof("Reconnected to scheduler at address %s", client.address)
	return nil
}

// NextClient returns the next client in a round-robin manner.
func (m *Manager) NextClient() schedulerv1pb.SchedulerClient {
	m.lock.Lock()
	defer m.lock.Unlock()

	// Check if there is only one client available
	if len(m.clients) == 1 {
		return m.clients[0].scheduler
	}

	// Randomly choose the starting index && get the next client
	//nolint:gosec // there is no need for a crypto secure rand.
	startingIndex := rand.Intn(len(m.clients))
	nextIdx := (m.lastUsedIdx + startingIndex) % len(m.clients)
	nextClient := m.clients[nextIdx]

	// Update the last used index
	m.lastUsedIdx = nextIdx

	return nextClient.scheduler
}
