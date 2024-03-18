package connections

import (
	"context"
	"fmt"
	"sync"
	"time"

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
	lock      sync.RWMutex
	connected []*scheduler.SidecarConnDetails
}

// Add adds a connection to the pool for a given namespace/appID.
func (p *Pool) Add(nsAppID string, connDetails *scheduler.SidecarConnDetails) {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		id.lock.Lock()
		defer id.lock.Unlock()
		id.connected = append(id.connected, connDetails)
	} else {
		p.NsAppIDPool[nsAppID] = &AppIDPool{
			connected: []*scheduler.SidecarConnDetails{connDetails},
		}
	}
}

// Remove removes a connection from the pool for a given namespace/appID.
func (p *Pool) Remove(nsAppID string, connDetails *scheduler.SidecarConnDetails) {
	p.Lock.Lock()
	defer p.Lock.Unlock()
	if id, ok := p.NsAppIDPool[nsAppID]; ok {
		id.lock.Lock()
		defer id.lock.Unlock()
		for i, c := range id.connected {
			if c == connDetails {
				id.connected = append(id.connected[:i], id.connected[i+1:]...)
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
		currentConnCount := len(p.NsAppIDPool[nsAppID].connected)
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
