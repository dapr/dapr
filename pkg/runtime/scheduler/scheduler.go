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
	"sync"
	"time"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	schedulerclient "github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

type Scheduler struct {
	Addresses    []string
	SidecarNS    string
	SidecarAppID string
	Sec          security.Handler
}

// Client represents a client for interacting with the scheduler.
type Client struct {
	conn       *grpc.ClientConn
	scheduler  schedulerv1pb.SchedulerClient
	address    string
	secHandler security.Handler
}

// Manager manages connections to multiple schedulers.
type Manager struct {
	Clients     []*Client
	ConnDetails scheduler.SidecarConnDetails
	Lock        sync.Mutex
	LastUsedIdx int
	wg          sync.WaitGroup
}

func NewManager(ctx context.Context, sched Scheduler) *Manager {
	manager := &Manager{
		ConnDetails: scheduler.SidecarConnDetails{
			Namespace: sched.SidecarNS,
			AppID:     sched.SidecarAppID,
		},
	}

	for _, address := range sched.Addresses {
		//maybe dont do this here and only do it in the run?
		conn, client, err := schedulerclient.New(ctx, address, sched.Sec)
		if err != nil {
			log.Infof("Scheduler client not initialized for address: %s", address)
		} else {
			log.Infof("Scheduler client initialized for address: %s", address)
		}

		manager.Clients = append(manager.Clients, &Client{
			conn:       conn,
			scheduler:  client,
			address:    address,
			secHandler: sched.Sec,
		})
	}

	return manager
}

// Run starts watching for job triggers from all scheduler clients.
func (m *Manager) Run(ctx context.Context) {
	m.wg.Add(len(m.Clients))

	// Start a goroutine for each client to watch for job updates
	for _, client := range m.Clients {
		go func(client *Client) {
			defer m.wg.Done()
			m.watchJob(ctx, client)
		}(client)
	}
}

// watchJob starts watching for job triggers from a single scheduler client.
func (m *Manager) watchJob(ctx context.Context, client *Client) {
	streamReq := &schedulerv1pb.StreamJobRequest{
		AppId:     m.ConnDetails.AppID,
		Namespace: m.ConnDetails.Namespace,
	}

	// TODO add retry logic without the lib
	// indefinitely stream with Scheduler
streamScheduler:
	for {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled. Exiting watchJob goroutine.") // TODO: rm this after debugging
			if client.conn != nil {
				if err := client.conn.Close(); err != nil {
					log.Infof("error closing scheduler client connection: %v", err)
				}
			}
			return
		default:
			// sidecar started before scheduler
			if client.conn == nil || client.scheduler == nil {
				if err := client.closeAndReconnect(ctx); err != nil {
					log.Infof("Error connecting to client: %v", err)
					// If reconnect fails, switch to the next client and retry
					client.scheduler = m.NextClient()
					time.Sleep(5 * time.Second)
					continue streamScheduler
				}
				//time.Sleep(5 * time.Second)
				//continue
			}

			stream, err := client.scheduler.WatchJob(ctx, streamReq)
			if err != nil {
				log.Infof("Error while streaming with Scheduler: %v", err) // TODO: rm this after debugging
				if err := client.closeAndReconnect(ctx); err != nil {
					log.Infof("Error reconnecting client: %v", err)
					// If reconnect fails, switch to the next client and retry
					client.scheduler = m.NextClient()
					time.Sleep(5 * time.Second)
					continue streamScheduler
				}
				time.Sleep(5 * time.Second)
				continue
			}

			log.Infof("Connected to Scheduler at address %s", client.address)

			// process streamed jobs
			for {
				select {
				case <-stream.Context().Done():
					log.Infof("Stream closed") // TODO: rm this after debugging
					if err := stream.CloseSend(); err != nil {
						log.Errorf("Error closing stream")
					}
					time.Sleep(5 * time.Second)
					continue streamScheduler // Exit inner loop and retry the connection
				case <-ctx.Done():
					log.Infof("Context cancelled") // TODO: rm this after debugging
					if err := stream.CloseSend(); err != nil {
						log.Errorf("Error closing stream")
					}
					continue streamScheduler // Exit inner loop and retry the connection
				default:
					// TODO: add resiliency policy for scheduler
					resp, err := stream.Recv()
					if err != nil {
						if status.Code(err) == codes.Canceled || status.Code(err) == codes.Unavailable || err == io.EOF {
							log.Infof("Scheduler cancelled the stream ctx.")
							// get a new client here
						} else {
							log.Errorf("Error while receiving job trigger: %v", err)
						}

						if err := stream.CloseSend(); err != nil {
							log.Errorf("Error closing stream")
						}
						time.Sleep(5 * time.Second)
						continue streamScheduler // Exit inner loop and retry the connection
					}
					log.Infof("Received response: %+v", resp)
				}
			}
		}
	}
}

// closeAndReconnect closes the connection and reconnects the client.
func (client *Client) closeAndReconnect(ctx context.Context) error {
	if client.conn != nil {
		if err := client.conn.Close(); err != nil {
			return fmt.Errorf("error closing connection: %v", err)
		}
	}
	conn, schedulerClient, err := schedulerclient.New(ctx, client.address, client.secHandler)
	if err != nil {
		return fmt.Errorf("error creating scheduler client for address %s: %v", client.address, err)
	}
	client.conn = conn
	client.scheduler = schedulerClient

	log.Infof("Reconnected to scheduler at address %s", client.address)
	return nil
}

// NextClient returns the next client in a round-robin manner.
func (m *Manager) NextClient() schedulerv1pb.SchedulerClient {
	m.Lock.Lock()
	defer m.Lock.Unlock()

	// Check if there is only one client available
	if len(m.Clients) == 1 {
		return m.Clients[0].scheduler
	}

	// Get the next client
	nextIdx := (m.LastUsedIdx + 1) % len(m.Clients)
	nextClient := m.Clients[nextIdx]

	// Update the last used index
	m.LastUsedIdx = nextIdx

	return nextClient.scheduler
}
