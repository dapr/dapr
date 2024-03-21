package connections

import (
	"context"
	"fmt"
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
	MinConnsPerAppID map[string]int
}

// AppIDPool represents a pool of connections for a single appID.
type AppIDPool struct {
	lock        sync.RWMutex
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
	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		id.lock.Lock()
		defer id.lock.Unlock()
		id.connections = append(id.connections, &Connection{
			ConnDetails: conn.ConnDetails,
			Stream:      conn.Stream,
		})
	} else {
		p.NsAppIDPool[nsAppID] = &AppIDPool{
			connections: []*Connection{&Connection{
				ConnDetails: conn.ConnDetails,
				Stream:      conn.Stream,
			}},
		}
	}
}

// Remove removes a connection from the pool for a given namespace/appID.
func (p *Pool) Remove(nsAppID string, conn *Connection) {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		id.lock.Lock()
		defer id.lock.Unlock()
		for i, c := range id.connections {
			if c.ConnDetails == conn.ConnDetails {
				// Remove the connection and stream from the slice
				id.connections = append(id.connections[:i], id.connections[i+1:]...)
				break
			}
		}
	}
}

// WaitUntilReachingMinConns waits until the minimum connection count (of sidecars) is reached for a given namespace/appID.
func (p *Pool) WaitUntilReachingMinConns(ctx context.Context, nsAppID string, minConnPerApp int, maxWaitTime time.Duration) error {
	timeout := time.After(maxWaitTime)

	for {
		p.Lock.RLock()
		currentConnCount := len(p.NsAppIDPool[nsAppID].connections)
		p.Lock.RUnlock()

		// We don't want all sidecars connecting to the scheduler, so return once we meet the min connection count.
		// This also accounts for enabling us to NOT have downtime, as we are waiting for the user specified minimum
		// connection count of sidecars -> schedulers.
		if currentConnCount >= minConnPerApp {
			log.Infof("Sufficient number of Sidecar connections to Scheduler reached for namespace/appID: %s. Current connection count: %d", nsAppID, currentConnCount)
			return nil // exit
		}

		// Check if the context is done
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout:
			return fmt.Errorf("timeout waiting for minimum connection count")
		case <-time.After(maxWaitTime):
			// Continue checking until the timeout or context is done
		}
	}
}

// Clear removes all connections from the pool.
func (p *Pool) Clear() {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	for _, appIDPool := range p.NsAppIDPool {
		appIDPool.lock.Lock()
		appIDPool.connections = []*Connection{}
		appIDPool.lock.Unlock()
	}

	p.NsAppIDPool = make(map[string]*AppIDPool)
}

// GetStreamAndContextForAppID returns a stream and its associated context corresponding to the given appID in a round-robin manner.
func (p *Pool) GetStreamAndContextForNSAppID(nsAppID string) (schedulerv1pb.Scheduler_WatchJobServer, context.Context, error) {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	// Get the AppIDPool for the given appID
	appIDPool, ok := p.NsAppIDPool[nsAppID]
	if !ok || len(appIDPool.connections) == 0 {
		return nil, nil, fmt.Errorf("no connections available for appID: %s", nsAppID)
	}

	// Round-robin selection of connection
	selectedConnection := appIDPool.connections[0]                                // Select the first connection
	appIDPool.connections = append(appIDPool.connections[1:], selectedConnection) // Rotate the slice
	ctx := selectedConnection.Stream.Context()

	return selectedConnection.Stream, ctx, nil
}
