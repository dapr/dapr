// Copyright (C) MongoDB, Inc. 2017-present.
//
// Licensed under the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License. You may obtain
// a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

// Package topology contains types that handles the discovery, monitoring, and selection
// of servers. This package is designed to expose enough inner workings of service discovery
// and monitoring to allow low level applications to have fine grained control, while hiding
// most of the detailed implementation of the algorithms.
package topology // import "go.mongodb.org/mongo-driver/x/mongo/driver/topology"

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"fmt"

	"go.mongodb.org/mongo-driver/x/mongo/driver"
	"go.mongodb.org/mongo-driver/x/mongo/driver/address"
	"go.mongodb.org/mongo-driver/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/x/mongo/driver/dns"
	"go.mongodb.org/mongo-driver/x/mongo/driver/session"
)

// ErrSubscribeAfterClosed is returned when a user attempts to subscribe to a
// closed Server or Topology.
var ErrSubscribeAfterClosed = errors.New("cannot subscribe after closeConnection")

// ErrTopologyClosed is returned when a user attempts to call a method on a
// closed Topology.
var ErrTopologyClosed = errors.New("topology is closed")

// ErrTopologyConnected is returned whena  user attempts to Connect to an
// already connected Topology.
var ErrTopologyConnected = errors.New("topology is connected or connecting")

// ErrServerSelectionTimeout is returned from server selection when the server
// selection process took longer than allowed by the timeout.
var ErrServerSelectionTimeout = errors.New("server selection timeout")

// MonitorMode represents the way in which a server is monitored.
type MonitorMode uint8

// These constants are the available monitoring modes.
const (
	AutomaticMode MonitorMode = iota
	SingleMode
)

// Topology represents a MongoDB deployment.
type Topology struct {
	connectionstate int32

	cfg *config

	desc atomic.Value // holds a description.Topology

	dnsResolver *dns.Resolver

	done chan struct{}

	pollingDone       chan struct{}
	pollingwg         sync.WaitGroup
	rescanSRVInterval time.Duration
	pollHeartbeatTime atomic.Value // holds a bool

	fsm *fsm

	SessionPool *session.Pool

	// This should really be encapsulated into it's own type. This will likely
	// require a redesign so we can share a minimum of data between the
	// subscribers and the topology.
	subscribers         map[uint64]chan description.Topology
	currentSubscriberID uint64
	subscriptionsClosed bool
	subLock             sync.Mutex

	// We should redesign how we Connect and handle individal servers. This is
	// too difficult to maintain and it's rather easy to accidentally access
	// the servers without acquiring the lock or checking if the servers are
	// closed. This lock should also be an RWMutex.
	serversLock   sync.Mutex
	serversClosed bool
	servers       map[address.Address]*Server
}

// New creates a new topology.
func New(opts ...Option) (*Topology, error) {
	cfg, err := newConfig(opts...)
	if err != nil {
		return nil, err
	}

	t := &Topology{
		cfg:               cfg,
		done:              make(chan struct{}),
		pollingDone:       make(chan struct{}),
		rescanSRVInterval: 60 * time.Second,
		fsm:               newFSM(),
		subscribers:       make(map[uint64]chan description.Topology),
		servers:           make(map[address.Address]*Server),
		dnsResolver:       dns.DefaultResolver,
	}
	t.desc.Store(description.Topology{})

	if cfg.replicaSetName != "" {
		t.fsm.SetName = cfg.replicaSetName
		t.fsm.Kind = description.ReplicaSetNoPrimary
	}

	if cfg.mode == SingleMode {
		t.fsm.Kind = description.Single
	}

	return t, nil
}

// Connect initializes a Topology and starts the monitoring process. This function
// must be called to properly monitor the topology.
func (t *Topology) Connect() error {
	if !atomic.CompareAndSwapInt32(&t.connectionstate, disconnected, connecting) {
		return ErrTopologyConnected
	}

	t.desc.Store(description.Topology{})
	var err error
	t.serversLock.Lock()
	for _, a := range t.cfg.seedList {
		addr := address.Address(a).Canonicalize()
		t.fsm.Servers = append(t.fsm.Servers, description.Server{Addr: addr})
		err = t.addServer(addr)
	}
	t.serversLock.Unlock()

	if srvPollingRequired(t.cfg.cs.Original) {
		go t.pollSRVRecords()
		t.pollingwg.Add(1)
	}

	t.subscriptionsClosed = false // explicitly set in case topology was disconnected and then reconnected

	atomic.StoreInt32(&t.connectionstate, connected)

	// After connection, make a subscription to keep the pool updated
	sub, err := t.Subscribe()
	t.SessionPool = session.NewPool(sub.C)
	return err
}

// Disconnect closes the topology. It stops the monitoring thread and
// closes all open subscriptions.
func (t *Topology) Disconnect(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&t.connectionstate, connected, disconnecting) {
		return ErrTopologyClosed
	}

	servers := make(map[address.Address]*Server)
	t.serversLock.Lock()
	t.serversClosed = true
	for addr, server := range t.servers {
		servers[addr] = server
	}
	t.serversLock.Unlock()

	for _, server := range servers {
		_ = server.Disconnect(ctx)
	}

	t.subLock.Lock()
	for id, ch := range t.subscribers {
		close(ch)
		delete(t.subscribers, id)
	}
	t.subscriptionsClosed = true
	t.subLock.Unlock()

	if srvPollingRequired(t.cfg.cs.Original) {
		t.pollingDone <- struct{}{}
		t.pollingwg.Wait()
	}

	t.desc.Store(description.Topology{})

	atomic.StoreInt32(&t.connectionstate, disconnected)
	return nil
}

func srvPollingRequired(connstr string) bool {
	return strings.HasPrefix(connstr, "mongodb+srv://")
}

// Description returns a description of the topology.
func (t *Topology) Description() description.Topology {
	td, ok := t.desc.Load().(description.Topology)
	if !ok {
		td = description.Topology{}
	}
	return td
}

// Kind returns the topology kind of this Topology.
func (t *Topology) Kind() description.TopologyKind { return t.Description().Kind }

// Subscribe returns a Subscription on which all updated description.Topologys
// will be sent. The channel of the subscription will have a buffer size of one,
// and will be pre-populated with the current description.Topology.
func (t *Topology) Subscribe() (*Subscription, error) {
	if atomic.LoadInt32(&t.connectionstate) != connected {
		return nil, errors.New("cannot subscribe to Topology that is not connected")
	}
	ch := make(chan description.Topology, 1)
	td, ok := t.desc.Load().(description.Topology)
	if !ok {
		td = description.Topology{}
	}
	ch <- td

	t.subLock.Lock()
	defer t.subLock.Unlock()
	if t.subscriptionsClosed {
		return nil, ErrSubscribeAfterClosed
	}
	id := t.currentSubscriberID
	t.subscribers[id] = ch
	t.currentSubscriberID++

	return &Subscription{
		C:  ch,
		t:  t,
		id: id,
	}, nil
}

// RequestImmediateCheck will send heartbeats to all the servers in the
// topology right away, instead of waiting for the heartbeat timeout.
func (t *Topology) RequestImmediateCheck() {
	if atomic.LoadInt32(&t.connectionstate) != connected {
		return
	}
	t.serversLock.Lock()
	for _, server := range t.servers {
		server.RequestImmediateCheck()
	}
	t.serversLock.Unlock()
}

// SupportsSessions returns true if the topology supports sessions.
func (t *Topology) SupportsSessions() bool {
	return t.Description().SessionTimeoutMinutes != 0 && t.Description().Kind != description.Single
}

// SupportsRetryWrites returns true if the topology supports retryable writes, which it does if it supports sessions.
func (t *Topology) SupportsRetryWrites() bool {
	return t.SupportsSessions()
}

// SelectServer selects a server with given a selector. SelectServer complies with the
// server selection spec, and will time out after severSelectionTimeout or when the
// parent context is done.
func (t *Topology) SelectServer(ctx context.Context, ss description.ServerSelector) (driver.Server, error) {
	if atomic.LoadInt32(&t.connectionstate) != connected {
		return nil, ErrTopologyClosed
	}
	var ssTimeoutCh <-chan time.Time

	if t.cfg.serverSelectionTimeout > 0 {
		ssTimeout := time.NewTimer(t.cfg.serverSelectionTimeout)
		ssTimeoutCh = ssTimeout.C
		defer ssTimeout.Stop()
	}

	sub, err := t.Subscribe()
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()

	for {
		suitable, err := t.selectServer(ctx, sub.C, ss, ssTimeoutCh)
		if err != nil {
			return nil, err
		}

		selected := suitable[rand.Intn(len(suitable))]
		selectedS, err := t.FindServer(selected)
		switch {
		case err != nil:
			return nil, err
		case selectedS != nil:
			return selectedS, nil
		default:
			// We don't have an actual server for the provided description.
			// This could happen for a number of reasons, including that the
			// server has since stopped being a part of this topology, or that
			// the server selector returned no suitable servers.
		}
	}
}

// SelectServerLegacy selects a server with given a selector. SelectServerLegacy complies with the
// server selection spec, and will time out after severSelectionTimeout or when the
// parent context is done.
func (t *Topology) SelectServerLegacy(ctx context.Context, ss description.ServerSelector) (*SelectedServer, error) {
	if atomic.LoadInt32(&t.connectionstate) != connected {
		return nil, ErrTopologyClosed
	}
	var ssTimeoutCh <-chan time.Time

	if t.cfg.serverSelectionTimeout > 0 {
		ssTimeout := time.NewTimer(t.cfg.serverSelectionTimeout)
		ssTimeoutCh = ssTimeout.C
		defer ssTimeout.Stop()
	}

	sub, err := t.Subscribe()
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()

	for {
		suitable, err := t.selectServer(ctx, sub.C, ss, ssTimeoutCh)
		if err != nil {
			return nil, err
		}

		selected := suitable[rand.Intn(len(suitable))]
		selectedS, err := t.FindServer(selected)
		switch {
		case err != nil:
			return nil, err
		case selectedS != nil:
			return selectedS, nil
		default:
			// We don't have an actual server for the provided description.
			// This could happen for a number of reasons, including that the
			// server has since stopped being a part of this topology, or that
			// the server selector returned no suitable servers.
		}
	}
}

// FindServer will attempt to find a server that fits the given server description.
// This method will return nil, nil if a matching server could not be found.
func (t *Topology) FindServer(selected description.Server) (*SelectedServer, error) {
	if atomic.LoadInt32(&t.connectionstate) != connected {
		return nil, ErrTopologyClosed
	}
	t.serversLock.Lock()
	defer t.serversLock.Unlock()
	server, ok := t.servers[selected.Addr]
	if !ok {
		return nil, nil
	}

	desc := t.Description()
	return &SelectedServer{
		Server: server,
		Kind:   desc.Kind,
	}, nil
}

func wrapServerSelectionError(err error, t *Topology) error {
	return fmt.Errorf("server selection error: %v\ncurrent topology: %s", err, t.String())
}

// selectServer is the core piece of server selection. It handles getting
// topology descriptions and running sever selection on those descriptions.
func (t *Topology) selectServer(ctx context.Context, subscriptionCh <-chan description.Topology, ss description.ServerSelector, timeoutCh <-chan time.Time) ([]description.Server, error) {
	var current description.Topology
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeoutCh:
			return nil, wrapServerSelectionError(ErrServerSelectionTimeout, t)
		case current = <-subscriptionCh:
		}

		var allowed []description.Server
		for _, s := range current.Servers {
			if s.Kind != description.Unknown {
				allowed = append(allowed, s)
			}
		}

		suitable, err := ss.SelectServer(current, allowed)
		if err != nil {
			return nil, wrapServerSelectionError(err, t)
		}

		if len(suitable) > 0 {
			return suitable, nil
		}

		t.RequestImmediateCheck()
	}
}

func (t *Topology) pollSRVRecords() {
	defer t.pollingwg.Done()

	serverConfig, _ := newServerConfig(t.cfg.serverOpts...)
	heartbeatInterval := serverConfig.heartbeatInterval

	pollTicker := time.NewTicker(t.rescanSRVInterval)
	defer pollTicker.Stop()
	t.pollHeartbeatTime.Store(false)
	var doneOnce bool
	defer func() {
		//  ¯\_(ツ)_/¯
		if r := recover(); r != nil && !doneOnce {
			<-t.pollingDone
		}
	}()

	// remove the scheme
	uri := t.cfg.cs.Original[14:]
	hosts := uri
	if idx := strings.IndexAny(uri, "/?@"); idx != -1 {
		hosts = uri[:idx]
	}

	for {
		select {
		case <-pollTicker.C:
		case <-t.pollingDone:
			doneOnce = true
			return
		}
		topoKind := t.Description().Kind
		if !(topoKind == description.Unknown || topoKind == description.Sharded) {
			break
		}

		parsedHosts, err := t.dnsResolver.ParseHosts(hosts, false)
		// DNS problem or no verified hosts returned
		if err != nil || len(parsedHosts) == 0 {
			if !t.pollHeartbeatTime.Load().(bool) {
				pollTicker.Stop()
				pollTicker = time.NewTicker(heartbeatInterval)
				t.pollHeartbeatTime.Store(true)
			}
			continue
		}
		if t.pollHeartbeatTime.Load().(bool) {
			pollTicker.Stop()
			pollTicker = time.NewTicker(t.rescanSRVInterval)
			t.pollHeartbeatTime.Store(false)
		}

		cont := t.processSRVResults(parsedHosts)
		if !cont {
			break
		}
	}
	<-t.pollingDone
	doneOnce = true
}

func (t *Topology) processSRVResults(parsedHosts []string) bool {
	t.serversLock.Lock()
	defer t.serversLock.Unlock()

	if t.serversClosed {
		return false
	}
	diff := t.fsm.Topology.DiffHostlist(parsedHosts)

	if len(diff.Added) == 0 && len(diff.Removed) == 0 {
		return true
	}

	for _, r := range diff.Removed {
		addr := address.Address(r).Canonicalize()
		s, ok := t.servers[addr]
		if !ok {
			continue
		}
		go func() {
			cancelCtx, cancel := context.WithCancel(context.Background())
			cancel()
			_ = s.Disconnect(cancelCtx)
		}()
		delete(t.servers, addr)
		t.fsm.removeServerByAddr(addr)
	}
	for _, a := range diff.Added {
		addr := address.Address(a).Canonicalize()
		_ = t.addServer(addr)
		t.fsm.addServer(addr)
	}
	//store new description
	newDesc := description.Topology{
		Kind:                  t.fsm.Kind,
		Servers:               t.fsm.Servers,
		SessionTimeoutMinutes: t.fsm.SessionTimeoutMinutes,
	}
	t.desc.Store(newDesc)

	t.subLock.Lock()
	for _, ch := range t.subscribers {
		// We drain the description if there's one in the channel
		select {
		case <-ch:
		default:
		}
		ch <- newDesc
	}
	t.subLock.Unlock()

	return true

}

func (t *Topology) apply(ctx context.Context, desc description.Server) {
	var err error

	t.serversLock.Lock()
	defer t.serversLock.Unlock()

	if _, ok := t.servers[desc.Addr]; t.serversClosed || !ok {
		return
	}

	prev := t.fsm.Topology

	current, err := t.fsm.apply(desc)
	if err != nil {
		return
	}

	diff := description.DiffTopology(prev, current)

	for _, removed := range diff.Removed {
		if s, ok := t.servers[removed.Addr]; ok {
			go func() {
				cancelCtx, cancel := context.WithCancel(ctx)
				cancel()
				_ = s.Disconnect(cancelCtx)
			}()
			delete(t.servers, removed.Addr)
		}
	}

	for _, added := range diff.Added {
		_ = t.addServer(added.Addr)
	}

	t.desc.Store(current)

	t.subLock.Lock()
	for _, ch := range t.subscribers {
		// We drain the description if there's one in the channel
		select {
		case <-ch:
		default:
		}
		ch <- current
	}
	t.subLock.Unlock()

}

func (t *Topology) addServer(addr address.Address) error {
	if _, ok := t.servers[addr]; ok {
		return nil
	}

	topoFunc := func(desc description.Server) {
		t.apply(context.TODO(), desc)
	}
	svr, err := ConnectServer(addr, topoFunc, t.cfg.serverOpts...)
	if err != nil {
		return err
	}

	t.servers[addr] = svr

	return nil
}

// String implements the Stringer interface
func (t *Topology) String() string {
	desc := t.Description()
	str := fmt.Sprintf("Type: %s\nServers:\n", desc.Kind)
	t.serversLock.Lock()
	defer t.serversLock.Unlock()
	for _, s := range t.servers {
		str += s.String() + "\n"
	}
	return str
}

// Subscription is a subscription to updates to the description of the Topology that created this
// Subscription.
type Subscription struct {
	C  <-chan description.Topology
	t  *Topology
	id uint64
}

// Unsubscribe unsubscribes this Subscription from updates and closes the
// subscription channel.
func (s *Subscription) Unsubscribe() error {
	s.t.subLock.Lock()
	defer s.t.subLock.Unlock()
	if s.t.subscriptionsClosed {
		return nil
	}

	ch, ok := s.t.subscribers[s.id]
	if !ok {
		return nil
	}

	close(ch)
	delete(s.t.subscribers, s.id)

	return nil
}
