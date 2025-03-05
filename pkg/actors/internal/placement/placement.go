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
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/dapr/pkg/actors/api"
	"github.com/dapr/dapr/pkg/actors/internal/apilevel"
	"github.com/dapr/dapr/pkg/actors/internal/placement/client"
	"github.com/dapr/dapr/pkg/actors/internal/reminders/storage"
	"github.com/dapr/dapr/pkg/actors/table"
	"github.com/dapr/dapr/pkg/actors/targets"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/concurrency/fifo"
	"github.com/dapr/kit/concurrency/lock"
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
	Lock(context.Context) (context.Context, context.CancelFunc, error)
	LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, error)
}

type Options struct {
	AppID     string
	Namespace string
	Hostname  string
	Port      int
	Addresses []string

	APILevel  *apilevel.APILevel
	Security  security.Handler
	Table     table.Interface
	Reminders storage.Interface
	Healthz   healthz.Healthz
}

type placement struct {
	client     *client.Client
	actorTable table.Interface
	reminders  storage.Interface
	apiLevel   *apilevel.APILevel
	htarget    healthz.Target

	hashTable         *hashing.ConsistentHashTables
	virtualNodesCache *hashing.VirtualNodesCache

	lock          *lock.OuterCancel
	lockVersion   atomic.Uint64
	updateVersion atomic.Uint64
	operationLock *fifo.Mutex

	tableUnlock context.CancelFunc

	appID     string
	namespace string
	hostname  string
	port      string
	isReady   atomic.Bool
	readyCh   chan struct{}
	wg        sync.WaitGroup
	closed    atomic.Bool
}

func New(opts Options) (Interface, error) {
	lock := lock.NewOuterCancel(errors.New("placement is disseminating"), time.Second*2)
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
		reminders:     opts.Reminders,
		appID:         opts.AppID,
		port:          strconv.Itoa(opts.Port),
		namespace:     opts.Namespace,
		hostname:      opts.Hostname,
		operationLock: fifo.New(),
		apiLevel:      opts.APILevel,
		htarget:       opts.Healthz.AddTarget(),
		lock:          lock,
		readyCh:       make(chan struct{}),
	}, nil
}

func (p *placement) Run(ctx context.Context) error {
	err := concurrency.NewRunnerManager(
		p.client.Run,
		func(ctx context.Context) error {
			p.lock.Run(ctx)
			return nil
		},
		func(ctx context.Context) error {
			ch, actorTypes := p.actorTable.SubscribeToTypeUpdates(ctx)
			log.Infof("Reporting actor types: %v", actorTypes)
			if err := p.sendHost(ctx, actorTypes); err != nil {
				return err
			}

			tch := time.NewTicker(statusReportHeartbeatInterval)
			defer tch.Stop()

			for {
				select {
				case actorTypes = <-ch:
					log.Infof("Updating actor types: %v", actorTypes)
					// TODO: @joshvanl: it is a nonsense to be sending the same actor
					// types over and over again, *every second*. This should be removed
					// ASAP.
				case <-tch.C:
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

	p.operationLock.Lock()
	if p.tableUnlock != nil {
		p.tableUnlock()
		p.tableUnlock = nil
	}
	p.operationLock.Unlock()

	return err
}

// LookupActor returns the address of the actor.
// Placement _must_ be locked before calling this method.
func (p *placement) LookupActor(ctx context.Context, req *api.LookupActorRequest) (*api.LookupActorResponse, error) {
	table, ok := p.hashTable.Entries[req.ActorType]
	if !ok {
		return nil, messages.ErrActorNoAddress
	}

	host, err := table.GetHost(req.ActorID)
	if err != nil {
		return nil, err
	}

	return &api.LookupActorResponse{
		Address: host.Name,
		AppID:   host.AppID,
		Local:   p.isActorLocal(host.Name, p.hostname, p.port),
	}, nil
}

func (p *placement) Ready() bool {
	return p.client.Ready()
}

func (p *placement) sendHost(ctx context.Context, actorTypes []string) error {
	err := p.client.Send(ctx, &v1pb.Host{
		Name:      p.hostname + ":" + p.port,
		Entities:  actorTypes,
		Id:        p.appID,
		ApiLevel:  20,
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
		p.handleLockOperation(ctx)
	case updateOperation:
		p.handleUpdateOperation(ctx, in.GetTables())
	case unlockOperation:
		p.handleUnlockOperation(ctx)
	}
}

func (p *placement) Lock(ctx context.Context) (context.Context, context.CancelFunc, error) {
	select {
	case <-p.readyCh:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	return p.lock.RLock(ctx)
}

func (p *placement) handleLockOperation(ctx context.Context) {
	p.tableUnlock = p.lock.Lock()
	lockVersion := p.lockVersion.Add(1)

	clear(p.hashTable.Entries)

	// If we don't receive an unlock in 10 seconds, unlock the table.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		select {
		case <-ctx.Done():
		case <-time.After(time.Second * 15):
			p.operationLock.Lock()
			defer p.operationLock.Unlock()
			if p.updateVersion.Load() < lockVersion {
				p.updateVersion.Store(lockVersion)
				clear(p.hashTable.Entries)
				if p.tableUnlock != nil {
					p.tableUnlock()
					p.tableUnlock = nil
				}
			}
		}
	}()
}

func (p *placement) handleUpdateOperation(ctx context.Context, in *v1pb.PlacementTables) {
	p.apiLevel.Set(in.GetApiLevel())

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

	if p.reminders != nil {
		p.reminders.DrainRebalancedReminders()
	}

	err := p.actorTable.Drain(func(target targets.Interface) bool {
		lar, err := p.LookupActor(ctx, &api.LookupActorRequest{
			ActorType: target.Type(),
			ActorID:   target.ID(),
		})
		if err != nil {
			log.Errorf("failed to lookup actor %s/%s: %s", target.Type(), target.ID(), err)
			return true
		}

		return lar != nil && !p.isActorLocal(lar.Address, p.hostname, p.port)
	})
	if err != nil {
		log.Errorf("Error draining actors: %s", err)
	}

	log.Infof("Placement tables updated, version: %s", in.GetVersion())
}

func (p *placement) handleUnlockOperation(ctx context.Context) {
	p.updateVersion.Add(1)

	found := true
	for _, actorType := range p.actorTable.Types() {
		if _, ok := p.hashTable.Entries[actorType]; !ok {
			found = false
			break
		}
	}

	if found {
		if p.reminders != nil {
			p.reminders.OnPlacementTablesUpdated(ctx, func(ctx context.Context, req *api.LookupActorRequest) bool {
				if ctx.Err() != nil {
					return false
				}
				lar, err := p.LookupActor(ctx, req)
				return err == nil && lar.Local
			})
		}

		if p.isReady.CompareAndSwap(false, true) {
			close(p.readyCh)
		}
	}

	p.htarget.Ready()
	p.tableUnlock()
	p.tableUnlock = nil
}

func (p *placement) isActorLocal(targetActorAddress, hostAddress string, port string) bool {
	if targetActorAddress == hostAddress+":"+port {
		// Easy case when there is a perfect match
		return true
	}

	if utils.IsLocalhost(hostAddress) && strings.HasSuffix(targetActorAddress, ":"+port) {
		return utils.IsLocalhost(targetActorAddress[0 : len(targetActorAddress)-len(port)-1])
	}

	return false
}
