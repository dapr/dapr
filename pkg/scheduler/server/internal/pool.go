package internal

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Pool represents a connection pool for namespace/appID separation of sidecars to schedulers.
type Pool struct {
	Lock             sync.RWMutex
	NsAppIDPool      map[string]*AppIDPool
	MaxConnsPerAppID int // future expand to diff conn count for diff appIDs?
}

// AppIDPool represents a pool of connections for a single appID.
type AppIDPool struct {
	connections []*Connection
}

type Connection struct {
	ConnDetails *scheduler.SidecarConnDetails
	Stream      schedulerv1pb.Scheduler_WatchJobServer
}

// Add adds a connection to the pool for a given namespace/appID.
func (p *Pool) Add(nsAppID string, conn *Connection) {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	// Ensure the AppIDPool exists for the given nsAppID
	if p.NsAppIDPool[nsAppID] == nil {
		p.NsAppIDPool[nsAppID] = &AppIDPool{
			connections: make([]*Connection, 0),
		}
	}

	// Check if adding the connection would exceed the maximum connection count only if it's explicitly set
	if p.MaxConnsPerAppID != -1 {
		if len(p.NsAppIDPool[nsAppID].connections) >= p.MaxConnsPerAppID {
			log.Infof("Sufficient number of Sidecar connections to Scheduler reached. Not adding connection for namespace/appID: %s. Current connection count: %d", nsAppID, len(p.NsAppIDPool[nsAppID].connections))
			return
		}
	}

	// Check if the connection already exists in the pool
	for _, existingConn := range p.NsAppIDPool[nsAppID].connections {
		if existingConn.ConnDetails == conn.ConnDetails {
			log.Infof("Not adding connection for namespace/appID: %s. Connection already exists.", nsAppID)
			return
		}
	}

	p.NsAppIDPool[nsAppID].connections = append(p.NsAppIDPool[nsAppID].connections, &Connection{
		ConnDetails: conn.ConnDetails,
		Stream:      conn.Stream,
	})
}

// Remove removes a connection from the pool for a given namespace/appID.
func (p *Pool) Remove(nsAppID string, conn *Connection) {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		for i, c := range id.connections {
			if c.ConnDetails == conn.ConnDetails {
				id.connections = append(id.connections[:i], id.connections[i+1:]...)
				break
			}
		}
	}
}

// WaitUntilReachingMaxConns waits until the minimum connection count (of sidecars) is reached for a given namespace/appID.
func (p *Pool) WaitUntilReachingMaxConns(ctx context.Context, nsAppID string, maxConnPerApp int, maxWaitTime time.Duration) error {
	timeout := time.After(maxWaitTime)

	for {
		p.Lock.RLock()
		appIDPool, ok := p.NsAppIDPool[nsAppID]
		if !ok {
			return fmt.Errorf("no connections available for appID: %s", nsAppID)
		}
		currentConnCount := len(appIDPool.connections)
		p.Lock.RUnlock()

		// We don't want all sidecars connecting to the scheduler, so return once we meet the max connection count.
		// This also accounts for enabling us to NOT have downtime, as we are waiting for the user specified max
		// connection count of sidecars per appID.
		if currentConnCount >= maxConnPerApp {
			log.Infof("Sufficient number of Sidecar connections to Scheduler reached for namespace/appID: %s. Current connection count: %d", nsAppID, currentConnCount)
			return nil
		}

		// Check if the context is done
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting to reach max sidecar connection count")
		case <-time.After(maxWaitTime):
			// Continue checking until the timeout or context is done
		}
	}
}

// Cleanup removes all connections from the pool.
func (p *Pool) Cleanup() {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	for _, appIDPool := range p.NsAppIDPool {
		appIDPool.connections = []*Connection{}
	}

	p.NsAppIDPool = make(map[string]*AppIDPool)
}

// GetStreamAndContextForNSAppID returns a stream and its associated context corresponding to the given appID in a round-robin manner.
func (p *Pool) GetStreamAndContextForNSAppID(nsAppID string) (schedulerv1pb.Scheduler_WatchJobServer, context.Context, error) {
	p.Lock.RLock()
	defer p.Lock.RUnlock()

	// Get the AppIDPool for the given appID
	appIDPool, ok := p.NsAppIDPool[nsAppID]
	if !ok || len(appIDPool.connections) == 0 {
		return nil, nil, fmt.Errorf("no connections available for appID: %s", nsAppID)
	}

	// randomly select the appID connection to stream back to
	//nolint:gosec // there is no need for a crypto secure rand.
	selectedConnection := appIDPool.connections[rand.Intn(len(appIDPool.connections))]
	ctx := selectedConnection.Stream.Context()

	return selectedConnection.Stream, ctx, nil
}
