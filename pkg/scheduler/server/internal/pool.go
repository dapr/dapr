package internal

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Pool represents a connection pool for namespace/appID separation of sidecars to schedulers.
type Pool struct {
	Lock        sync.RWMutex
	NsAppIDPool map[string]*AppIDPool
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
func (p *Pool) GetStreamAndContextForNSAppID(ns, appID string) (schedulerv1pb.Scheduler_WatchJobServer, context.Context, error) {
	p.Lock.RLock()
	defer p.Lock.RUnlock()

	// Get the AppIDPool for the given appID
	appIDPool, ok := p.NsAppIDPool[ns+appID]
	if !ok || len(appIDPool.connections) == 0 {
		return nil, nil, fmt.Errorf("no connections available for appID: %s", ns+appID)
	}

	// randomly select the appID connection to stream back to
	//nolint:gosec // there is no need for a crypto secure rand.
	selectedConnection := appIDPool.connections[rand.Intn(len(appIDPool.connections))]
	ctx := selectedConnection.Stream.Context()

	return selectedConnection.Stream, ctx, nil
}
