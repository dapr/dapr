package internal

import (
	"fmt"
	"math/rand"
	"sync"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
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
	Namespace string
	AppID     string
	Stream    schedulerv1pb.Scheduler_WatchJobsServer
}

// Add adds a connection to the pool for a given namespace/appID.
func (p *Pool) Add(ns, appID string, conn *Connection) {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	nsAppID := ns + "||" + appID
	// Ensure the AppIDPool exists for the given nsAppID
	if p.NsAppIDPool[nsAppID] == nil {
		p.NsAppIDPool[nsAppID] = &AppIDPool{
			connections: make([]*Connection, 0),
		}
	}

	// Check if the connection already exists in the pool
	for _, existingConn := range p.NsAppIDPool[nsAppID].connections {
		if (existingConn.Namespace == conn.Namespace) && (existingConn.AppID == conn.AppID) {
			log.Infof("Not adding connection for namespace/appID: %s. Connection already exists.", nsAppID)
			return
		}
	}

	p.NsAppIDPool[nsAppID].connections = append(p.NsAppIDPool[nsAppID].connections, &Connection{
		Namespace: conn.Namespace,
		AppID:     conn.AppID,
		Stream:    conn.Stream,
	})
}

// Remove removes a connection from the pool for a given namespace/appID.
func (p *Pool) Remove(ns, appID string, conn *Connection) {
	p.Lock.Lock()
	defer p.Lock.Unlock()

	nsAppID := ns + "||" + appID
	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		for i, c := range id.connections {
			if (c.Namespace == conn.Namespace) && (c.AppID == conn.AppID) {
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
func (p *Pool) GetStreamAndContextForNSAppID(ns, appID string) (schedulerv1pb.Scheduler_WatchJobsServer, error) {
	p.Lock.RLock()
	defer p.Lock.RUnlock()

	nsAppID := ns + "||" + appID
	// Get the AppIDPool for the given appID
	appIDPool, ok := p.NsAppIDPool[nsAppID]
	if !ok || len(appIDPool.connections) == 0 {
		return nil, fmt.Errorf("no connections available for appID: %s", nsAppID)
	}

	// randomly select the appID connection to stream back to
	//nolint:gosec // there is no need for a crypto secure rand.
	selectedConnection := appIDPool.connections[rand.Intn(len(appIDPool.connections))]
	return selectedConnection.Stream, nil
}
