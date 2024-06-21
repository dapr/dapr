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

package internal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"google.golang.org/protobuf/types/known/anypb"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.scheduler")

// Pool represents a connection pool for namespace/appID separation of sidecars to schedulers.
type Pool struct {
	nsPool map[string]*namespacedPool

	lock    sync.RWMutex
	wg      sync.WaitGroup
	closeCh chan struct{}
	running atomic.Bool
}

type namespacedPool struct {
	idx       atomic.Uint64
	appID     map[string][]uint64
	actorType map[string][]uint64
	conns     map[uint64]*conn
}

// JobEvent is a triggered job event.
type JobEvent struct {
	Name     string
	Data     *anypb.Any
	Metadata *schedulerv1pb.JobMetadata
}

func NewPool() *Pool {
	return &Pool{
		nsPool:  make(map[string]*namespacedPool),
		closeCh: make(chan struct{}),
	}
}

func (p *Pool) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("pool is already running")
	}

	<-ctx.Done()
	close(p.closeCh)
	p.wg.Wait()

	return nil
}

// Add adds a connection to the pool for a given namespace/appID.
func (p *Pool) Add(req *schedulerv1pb.WatchJobsRequestInitial, stream schedulerv1pb.Scheduler_WatchJobsServer) {
	p.lock.Lock()
	defer p.lock.Unlock()

	nsPool, ok := p.nsPool[req.GetNamespace()]
	if !ok {
		nsPool = &namespacedPool{
			appID:     make(map[string][]uint64),
			actorType: make(map[string][]uint64),
			conns:     make(map[uint64]*conn),
		}

		p.nsPool[req.GetNamespace()] = nsPool
	}

	ok = true
	var id uint64
	for ok {
		id++
		_, ok = nsPool.conns[id]
	}

	log.Debugf("Adding a Sidecar connection to Scheduler for appID: %s/%s.", req.GetNamespace(), req.GetAppId())
	nsPool.appID[req.GetAppId()] = append(nsPool.appID[req.GetAppId()], id)

	for _, actorType := range req.GetActorTypes() {
		log.Debugf("Adding a Sidecar connection to Scheduler for actor type: %s/%s.", req.GetNamespace(), actorType)
		nsPool.actorType[actorType] = append(nsPool.actorType[actorType], id)
	}

	nsPool.conns[id] = p.newConn(req, stream, id)
}

// Send is a blocking function that sends a job trigger to a correct job
// recipient.
func (p *Pool) Send(ctx context.Context, job *JobEvent) error {
	conn, err := p.getConn(job.Metadata)
	if err != nil {
		return err
	}

	conn.sendWaitForResponse(ctx, job)

	return nil
}

// remove removes a connection from the pool with the given UUID.
func (p *Pool) remove(req *schedulerv1pb.WatchJobsRequestInitial, id uint64) {
	p.lock.Lock()
	defer p.lock.Unlock()

	nsPool, ok := p.nsPool[req.GetNamespace()]
	if !ok {
		return
	}

	appIDConns, ok := nsPool.appID[req.GetAppId()]
	if !ok {
		return
	}

	delete(nsPool.conns, id)

	log.Infof("Removing a Sidecar connection from Scheduler for appID: %s/%s.", req.GetNamespace(), req.GetAppId())
	for i := 0; i < len(appIDConns); i++ {
		if appIDConns[i] == id {
			appIDConns = append(appIDConns[:i], appIDConns[i+1:]...)
			break
		}
	}

	nsPool.appID[req.GetAppId()] = appIDConns

	for _, actorType := range req.GetActorTypes() {
		actorTypeConns, ok := nsPool.actorType[actorType]
		if !ok {
			continue
		}

		log.Infof("Removing a Sidecar connection from Scheduler for actor type: %s/%s.", req.GetNamespace(), actorType)
		for i := 0; i < len(actorTypeConns); i++ {
			if actorTypeConns[i] == id {
				actorTypeConns = append(actorTypeConns[:i], actorTypeConns[i+1:]...)
				break
			}
		}

		nsPool.actorType[actorType] = actorTypeConns

		if len(nsPool.actorType[actorType]) == 0 {
			delete(nsPool.actorType, actorType)
		}
	}

	if len(nsPool.appID[req.GetAppId()]) == 0 {
		delete(nsPool.appID, req.GetAppId())
	}

	if len(nsPool.appID) == 0 && len(nsPool.actorType) == 0 {
		delete(p.nsPool, req.GetNamespace())
	}
}

// getConn returns a connection from the pool based on the metadata.
func (p *Pool) getConn(meta *schedulerv1pb.JobMetadata) (*conn, error) {
	p.lock.RLock()
	defer p.lock.RUnlock()

	nsPool, ok := p.nsPool[meta.GetNamespace()]
	if !ok {
		return nil, fmt.Errorf("no connections available for namespace: %s", meta.GetNamespace())
	}

	idx := nsPool.idx.Add(1)

	switch t := meta.GetTarget(); t.GetType().(type) {
	case *schedulerv1pb.JobTargetMetadata_Job:
		appIDConns, ok := nsPool.appID[meta.GetAppId()]
		if !ok || len(appIDConns) == 0 {
			return nil, fmt.Errorf("no connections available for appID: %s", meta.GetAppId())
		}
		conn := nsPool.conns[appIDConns[int(idx)%len(appIDConns)]]
		return conn, nil

	case *schedulerv1pb.JobTargetMetadata_Actor:
		actorTypeConns, ok := nsPool.actorType[t.GetActor().GetType()]
		if !ok || len(actorTypeConns) == 0 {
			return nil, fmt.Errorf("no connections available for actorType: %s", t.GetActor().GetType())
		}

		conn := nsPool.conns[actorTypeConns[int(idx)%len(actorTypeConns)]]
		return conn, nil

	default:
		return nil, fmt.Errorf("unknown job metadata type: %v", t)
	}
}
