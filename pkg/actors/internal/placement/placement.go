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

package placement

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/internal/placement/client"
	"github.com/dapr/dapr/pkg/actors/internal/placement/lock"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/requestresponse"
	"github.com/dapr/dapr/pkg/actors/table"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/events/batcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actors.placement")

const (
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"

	statusReportHeartbeatInterval = 1 * time.Second
)

type Interface interface {
	Run(context.Context) error
	Ready() bool
	Lock()
	Unlock()
	IsActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool
	LookupActor(ctx context.Context, req *requestresponse.LookupActorRequest) (*requestresponse.LookupActorResponse, error)
	SubscribeDisemination(ctx context.Context) <-chan struct{}
}

type Options struct {
	AppID     string
	Namespace string
	Hostname  string
	Port      int
	APILevel  uint32
	Addresses []string

	Security  security.Handler
	Table     table.Interface
	Reminders storage.Interface
	Healthz   healthz.Healthz
}

type placement struct {
	client     *client.Client
	actorTable table.Interface
	reminders  storage.Interface
	htarget    healthz.Target

	hashTable         *hashing.ConsistentHashTables
	virtualNodesCache *hashing.VirtualNodesCache

	lock               *lock.Lock
	lockVersion        atomic.Uint64
	updateVersion      atomic.Uint64
	operationLock      *fifo.Mutex
	disseminateBatcher *batcher.Batcher[int, struct{}]

	appID     string
	namespace string
	hostname  string
	port      int
	// TODO: @joshvanl
	apiLevel uint32
	wg       sync.WaitGroup
	closed   atomic.Bool
}

func New(opts Options) (Interface, error) {
	lock := lock.New()
	client, err := client.New(client.Options{
		Addresses: opts.Addresses,
		Security:  opts.Security,
		Table:     opts.Table,
		Lock:      lock,
		Healthz:   opts.Healthz,
	})
	if err != nil {
		return nil, err
	}

	return &placement{
		client:            client,
		actorTable:        opts.Table,
		virtualNodesCache: hashing.NewVirtualNodesCache(),
		hashTable: &hashing.ConsistentHashTables{
			Entries: make(map[string]*hashing.Consistent),
		},
		reminders:          opts.Reminders,
		appID:              opts.AppID,
		port:               opts.Port,
		namespace:          opts.Namespace,
		hostname:           opts.Hostname,
		apiLevel:           opts.APILevel,
		operationLock:      fifo.New(),
		lock:               lock,
		htarget:            opts.Healthz.AddTarget(),
		disseminateBatcher: batcher.New[int, struct{}](time.Millisecond * 100),
	}, nil
}

func (p *placement) Run(ctx context.Context) error {
	err := concurrency.NewRunnerManager(
		p.client.Run,
		func(ctx context.Context) error {
			ch := p.actorTable.SubscribeToTypeUpdates(ctx)

			actorTypes := p.actorTable.Types()
			if err := p.sendHost(ctx, actorTypes); err != nil {
				return err
			}

			p.htarget.Ready()

			for {
				select {
				case actorTypes = <-ch:
					// TODO: @joshvanl: it is a nonsense to be sending the same actor
					// types over and over again, *every second*. This should be removed
					// ASAP.
				case <-time.After(statusReportHeartbeatInterval):
				case <-ctx.Done():
					return nil
				}

				if err := p.sendHost(ctx, actorTypes); err != nil {
					return err
				}
			}
		},
		func(ctx context.Context) error {
			defer p.wg.Wait()

			for {
				in, err := p.client.Recv(ctx)
				if err != nil {
					return err
				}

				p.handleReceive(ctx, in)
			}
		},
	).Run(ctx)

	p.closed.Store(true)
	p.disseminateBatcher.Close()
	p.lock.EnsureUnlockTable()
	return err
}

func (p *placement) SubscribeDisemination(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{})
	if p.closed.Load() {
		close(ch)
		return ch
	}

	p.disseminateBatcher.Subscribe(ctx, ch)
	return ch
}

func (p *placement) LookupActor(ctx context.Context, req *requestresponse.LookupActorRequest) (*requestresponse.LookupActorResponse, error) {
	p.lock.LockLookup()
	defer p.lock.UnlockLookup()

	table, ok := p.hashTable.Entries[req.ActorType]
	if !ok {
		return nil, fmt.Errorf("did not find address for actor %s/%s", req.ActorType, req.ActorID)

	}

	host, err := table.GetHost(req.ActorID)
	if err != nil {
		return nil, err
	}

	return &requestresponse.LookupActorResponse{
		Address: host.Name,
		AppID:   host.AppID,
	}, nil
}

func (p *placement) Ready() bool {
	return p.client.Ready()
}

func (p *placement) sendHost(ctx context.Context, actorTypes []string) error {
	log.Debugf("Reporting actor types: %v\n", actorTypes)

	err := p.client.Send(ctx, &v1pb.Host{
		Name:      p.hostname,
		Entities:  actorTypes,
		Id:        p.appID,
		ApiLevel:  p.apiLevel,
		Namespace: p.namespace,
	})
	if err != nil {
		diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
		return err
	}

	diag.DefaultMonitoring.ActorStatusReported("send")

	return nil
}

func (p *placement) handleReceive(ctx context.Context, in *v1pb.PlacementOrder) {
	p.operationLock.Lock()
	defer p.operationLock.Unlock()

	log.Debugf("Placement order received: %s", in.GetOperation())
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.GetOperation())

	switch in.GetOperation() {
	case lockOperation:
		p.handleLockOperation(ctx, in.GetTables())
	case updateOperation:
		p.handleUpdateOperation(ctx, in.GetTables())
	case unlockOperation:
		p.updateVersion.Add(1)
		p.lock.EnsureUnlockTable()
	}
}

func (p *placement) Lock() {
	p.lock.LockLookup()
}

func (p *placement) Unlock() {
	p.lock.UnlockLookup()
}

func (p *placement) handleLockOperation(ctx context.Context, tables *v1pb.PlacementTables) {
	p.disseminateBatcher.Batch(0, struct{}{})
	p.lock.LockTable()
	lockVersion := p.lockVersion.Add(1)

	// If we don't receive an unlock in 10 seconds, unlock the table.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second * 10):
			p.operationLock.Lock()
			defer p.operationLock.Unlock()
			if p.updateVersion.Load() < lockVersion {
				p.lock.EnsureUnlockTable()
			}
		}
	}()
}

func (p *placement) handleUpdateOperation(ctx context.Context, in *v1pb.PlacementTables) {
	if in.GetApiLevel() != p.apiLevel {
		p.apiLevel = in.GetApiLevel()
		log.Infof("Actor API level in the cluster has been updated to %d", p.apiLevel)
	}

	entries := make(map[string]*hashing.Consistent)

	for k, v := range in.GetEntries() {
		loadMap := make(map[string]*hashing.Host, len(v.GetLoadMap()))
		for lk, lv := range v.GetLoadMap() {
			loadMap[lk] = hashing.NewHost(lv.GetName(), lv.GetId(), lv.GetLoad(), lv.GetPort())
		}

		entries[k] = hashing.NewFromExisting(loadMap, in.GetReplicationFactor(), p.virtualNodesCache)
	}

	clear(p.hashTable.Entries)
	p.hashTable.Version = in.GetVersion()
	p.hashTable.Entries = entries

	p.actorTable.Drain(func(actorType, actorID string) bool {
		p.reminders.DrainRebalancedReminders(actorType, actorID)

		lar, err := p.LookupActor(ctx, &requestresponse.LookupActorRequest{
			ActorType: actorType,
			ActorID:   actorID,
		})
		if err != nil {
			log.Errorf("failed to lookup actor %s/%s: %s", actorType, actorID, err)
			return true
		}

		return lar != nil && !p.IsActorLocal(lar.Address, p.hostname, p.port)
	})

	p.reminders.OnPlacementTablesUpdated(ctx)

	log.Infof("Placement tables updated, version: %s", in.GetVersion())
}

func (p *placement) IsActorLocal(targetActorAddress, hostAddress string, grpcPort int) bool {
	portStr := strconv.Itoa(grpcPort)

	if targetActorAddress == hostAddress+":"+portStr {
		// Easy case when there is a perfect match
		return true
	}

	if isLocalhost(hostAddress) && strings.HasSuffix(targetActorAddress, ":"+portStr) {
		return isLocalhost(targetActorAddress[0 : len(targetActorAddress)-len(portStr)-1])
	}

	return false
}

func isLocalhost(addr string) bool {
	return addr == "localhost" || addr == "127.0.0.1" || addr == "[::1]" || addr == "::1"
}
