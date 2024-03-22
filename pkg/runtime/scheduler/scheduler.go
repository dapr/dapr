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
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	schedulerclient "github.com/dapr/dapr/pkg/scheduler/client"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
	"google.golang.org/grpc"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

type Scheduler struct {
	Addresses    []string
	SidecarAddr  string
	SidecarPort  int
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
	LastUsedIdx int            // Track the index of the last used client
	wg          sync.WaitGroup // Wait group for goroutine synchronization
}

// for as many addresses as I have, create a client and maintain those client connections,
// if 1 drops backoff & trigger reconnecting for all connections

func NewManager(ctx context.Context, sched Scheduler) (*Manager, error) {
	manager := &Manager{
		ConnDetails: scheduler.SidecarConnDetails{
			Namespace: sched.SidecarNS,
			Host:      sched.SidecarAddr,
			Port:      sched.SidecarPort,
			AppID:     sched.SidecarAppID,
		},
	}

	// Create a client for each scheduler address
	for _, address := range sched.Addresses {
		conn, client, err := schedulerclient.New(ctx, address, sched.Sec)

		if err != nil {
			return nil, fmt.Errorf("error creating scheduler client for address %s: %w", address, err)
		}
		log.Infof("Scheduler client initialized for address: %s\n", address)

		manager.Clients = append(manager.Clients, &Client{
			conn:       conn,
			scheduler:  client,
			address:    address,
			secHandler: sched.Sec,
		})
	}
	// close for loop
	return manager, nil
}

// Run starts watching for job triggers from all scheduler clients.
// func (m *Manager) Run(ctx context.Context) {
func (m *Manager) Run(ctx context.Context) {
	// Add the number of goroutines to the wait group
	m.wg.Add(len(m.Clients)) // schedulers will go up and down, need to fix this logic to acct for that

	// Start a goroutine for each client to watch for job updates
	for _, client := range m.Clients {
		// Increment the wait group counter before starting the goroutine
		//m.wg.Add(1)

		go func(client *Client) {
			defer m.wg.Done() // Decrement the wait group counter when the goroutine exits
			m.watchJob(ctx, client)
		}(client)
	}
}

// watchJob starts watching for job triggers from a single scheduler client.
func (m *Manager) watchJob(ctx context.Context, client *Client) {
	streamReq := &schedulerv1pb.StreamJobRequest{
		AppId:     m.ConnDetails.AppID,
		Namespace: m.ConnDetails.Namespace,
		Hostname:  m.ConnDetails.Host,
		Port:      int32(m.ConnDetails.Port),
	}

	// close the conn when watchJob exits
	defer func() {
		if err := client.conn.Close(); err != nil {
			log.Errorf("Error closing connection: %v", err)
		}
	}()

	backoffPolicy := backoff.NewExponentialBackOff()
	backoffPolicy.MaxElapsedTime = 0                 // Retry indefinitely
	backoffPolicy.InitialInterval = 30 * time.Second // Initial retry interval
	backoffPolicy.MaxInterval = 5 * time.Minute      // Max interval after which retries are capped

	err := backoff.Retry(func() error {
		select {
		case <-ctx.Done():
			log.Infof("Context cancelled. Exiting watchJob goroutine.")
			return backoff.Permanent(ctx.Err())
		default:
			stream, err := client.scheduler.WatchJob(ctx, streamReq)
			if err != nil {
				log.Errorf("Error while streaming with Scheduler: %v. Retrying...\n", err)
				return err // retryable error
			}

			for {
				select {
				case <-ctx.Done():
					log.Infof("Context cancelled. Exiting watchJob goroutine.")
					return nil
				default:
					resp, err := stream.Recv()
					if err != nil {
						log.Errorf("Error while receiving job updates: %v. Retrying...\n", err)
						return err // retryable error
					}
					log.Infof("Received response: %v", resp)
				}
			}
		}
	}, backoffPolicy)

	if err != nil {
		log.Errorf("Retry attempts exhausted. Closing connections and reconnecting.")
		m.closeAndReconnect(ctx)
	}
	//
	//for {
	//	select {
	//	case <-ctx.Done():
	//		log.Infof("Context cancelled. Exiting watchJob goroutine.")
	//		return
	//	default:
	//		stream, err := client.scheduler.WatchJob(ctx, streamReq)
	//		if err != nil {
	//			log.Errorf("Error while streaming with Scheduler: %v", err)
	//			m.closeAndReconnect(ctx)
	//			time.Sleep(5 * time.Second) // Sleep for 5 seconds before retrying
	//			continue
	//			//return
	//		}
	//
	//		for {
	//			select {
	//			case <-ctx.Done():
	//				log.Infof("Context cancelled. Exiting watchJob goroutine.")
	//				return
	//			default:
	//				resp, err := stream.Recv() // TODO: look at resilency policies on daprd
	//				if err != nil {
	//					log.Errorf("Error while receiving job updates: %v", err)
	//					break // Exit inner loop and retry the connection
	//
	//					//return // dont exit, backoff and retry conn
	//				}
	//				log.Infof("Received response: %v", resp)
	//			}
	//		}
	//	}
	//}
}

// closeAndReconnect closes all client connections and reconnects.
func (m *Manager) closeAndReconnect(ctx context.Context) {
	m.Lock.Lock()
	defer m.Lock.Unlock()

	for _, client := range m.Clients {
		if err := client.conn.Close(); err != nil {
			log.Errorf("Error closing connection: %v", err)
		}
		conn, schedulerClient, err := schedulerclient.New(ctx, client.address, client.secHandler)
		if err != nil {
			log.Errorf("Error creating scheduler client for address %s: %v", client.address, err)
			continue
		}
		client.conn = conn
		client.scheduler = schedulerClient
	}
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
