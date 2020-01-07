// Copyright (c) 2012 The gocql Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
//This file will be the future home for more policies
package gocql

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hailocab/go-hostpool"
)

// cowHostList implements a copy on write host list, its equivalent type is []*HostInfo
type cowHostList struct {
	list atomic.Value
	mu   sync.Mutex
}

func (c *cowHostList) String() string {
	return fmt.Sprintf("%+v", c.get())
}

func (c *cowHostList) get() []*HostInfo {
	// TODO(zariel): should we replace this with []*HostInfo?
	l, ok := c.list.Load().(*[]*HostInfo)
	if !ok {
		return nil
	}
	return *l
}

func (c *cowHostList) set(list []*HostInfo) {
	c.mu.Lock()
	c.list.Store(&list)
	c.mu.Unlock()
}

// add will add a host if it not already in the list
func (c *cowHostList) add(host *HostInfo) bool {
	c.mu.Lock()
	l := c.get()

	if n := len(l); n == 0 {
		l = []*HostInfo{host}
	} else {
		newL := make([]*HostInfo, n+1)
		for i := 0; i < n; i++ {
			if host.Equal(l[i]) {
				c.mu.Unlock()
				return false
			}
			newL[i] = l[i]
		}
		newL[n] = host
		l = newL
	}

	c.list.Store(&l)
	c.mu.Unlock()
	return true
}

func (c *cowHostList) update(host *HostInfo) {
	c.mu.Lock()
	l := c.get()

	if len(l) == 0 {
		c.mu.Unlock()
		return
	}

	found := false
	newL := make([]*HostInfo, len(l))
	for i := range l {
		if host.Equal(l[i]) {
			newL[i] = host
			found = true
		} else {
			newL[i] = l[i]
		}
	}

	if found {
		c.list.Store(&newL)
	}

	c.mu.Unlock()
}

func (c *cowHostList) remove(ip net.IP) bool {
	c.mu.Lock()
	l := c.get()
	size := len(l)
	if size == 0 {
		c.mu.Unlock()
		return false
	}

	found := false
	newL := make([]*HostInfo, 0, size)
	for i := 0; i < len(l); i++ {
		if !l[i].ConnectAddress().Equal(ip) {
			newL = append(newL, l[i])
		} else {
			found = true
		}
	}

	if !found {
		c.mu.Unlock()
		return false
	}

	newL = newL[: size-1 : size-1]
	c.list.Store(&newL)
	c.mu.Unlock()

	return true
}

// RetryableQuery is an interface that represents a query or batch statement that
// exposes the correct functions for the retry policy logic to evaluate correctly.
type RetryableQuery interface {
	Attempts() int
	SetConsistency(c Consistency)
	GetConsistency() Consistency
	Context() context.Context
}

type RetryType uint16

const (
	Retry         RetryType = 0x00 // retry on same connection
	RetryNextHost RetryType = 0x01 // retry on another connection
	Ignore        RetryType = 0x02 // ignore error and return result
	Rethrow       RetryType = 0x03 // raise error and stop retrying
)

// ErrUnknownRetryType is returned if the retry policy returns a retry type
// unknown to the query executor.
var ErrUnknownRetryType = errors.New("unknown retry type returned by retry policy")

// RetryPolicy interface is used by gocql to determine if a query can be attempted
// again after a retryable error has been received. The interface allows gocql
// users to implement their own logic to determine if a query can be attempted
// again.
//
// See SimpleRetryPolicy as an example of implementing and using a RetryPolicy
// interface.
type RetryPolicy interface {
	Attempt(RetryableQuery) bool
	GetRetryType(error) RetryType
}

// SimpleRetryPolicy has simple logic for attempting a query a fixed number of times.
//
// See below for examples of usage:
//
//     //Assign to the cluster
//     cluster.RetryPolicy = &gocql.SimpleRetryPolicy{NumRetries: 3}
//
//     //Assign to a query
//     query.RetryPolicy(&gocql.SimpleRetryPolicy{NumRetries: 1})
//
type SimpleRetryPolicy struct {
	NumRetries int //Number of times to retry a query
}

// Attempt tells gocql to attempt the query again based on query.Attempts being less
// than the NumRetries defined in the policy.
func (s *SimpleRetryPolicy) Attempt(q RetryableQuery) bool {
	return q.Attempts() <= s.NumRetries
}

func (s *SimpleRetryPolicy) GetRetryType(err error) RetryType {
	return RetryNextHost
}

// ExponentialBackoffRetryPolicy sleeps between attempts
type ExponentialBackoffRetryPolicy struct {
	NumRetries int
	Min, Max   time.Duration
}

func (e *ExponentialBackoffRetryPolicy) Attempt(q RetryableQuery) bool {
	if q.Attempts() > e.NumRetries {
		return false
	}
	time.Sleep(e.napTime(q.Attempts()))
	return true
}

// used to calculate exponentially growing time
func getExponentialTime(min time.Duration, max time.Duration, attempts int) time.Duration {
	if min <= 0 {
		min = 100 * time.Millisecond
	}
	if max <= 0 {
		max = 10 * time.Second
	}
	minFloat := float64(min)
	napDuration := minFloat * math.Pow(2, float64(attempts-1))
	// add some jitter
	napDuration += rand.Float64()*minFloat - (minFloat / 2)
	if napDuration > float64(max) {
		return time.Duration(max)
	}
	return time.Duration(napDuration)
}

func (e *ExponentialBackoffRetryPolicy) GetRetryType(err error) RetryType {
	return RetryNextHost
}

// DowngradingConsistencyRetryPolicy: Next retry will be with the next consistency level
// provided in the slice
//
// On a read timeout: the operation is retried with the next provided consistency
// level.
//
// On a write timeout: if the operation is an :attr:`~.UNLOGGED_BATCH`
// and at least one replica acknowledged the write, the operation is
// retried with the next consistency level.  Furthermore, for other
// write types, if at least one replica acknowledged the write, the
// timeout is ignored.
//
// On an unavailable exception: if at least one replica is alive, the
// operation is retried with the next provided consistency level.

type DowngradingConsistencyRetryPolicy struct {
	ConsistencyLevelsToTry []Consistency
}

func (d *DowngradingConsistencyRetryPolicy) Attempt(q RetryableQuery) bool {
	currentAttempt := q.Attempts()

	if currentAttempt > len(d.ConsistencyLevelsToTry) {
		return false
	} else if currentAttempt > 0 {
		q.SetConsistency(d.ConsistencyLevelsToTry[currentAttempt-1])
		if gocqlDebug {
			Logger.Printf("%T: set consistency to %q\n",
				d,
				d.ConsistencyLevelsToTry[currentAttempt-1])
		}
	}
	return true
}

func (d *DowngradingConsistencyRetryPolicy) GetRetryType(err error) RetryType {
	switch t := err.(type) {
	case *RequestErrUnavailable:
		if t.Alive > 0 {
			return Retry
		}
		return Rethrow
	case *RequestErrWriteTimeout:
		if t.WriteType == "SIMPLE" || t.WriteType == "BATCH" || t.WriteType == "COUNTER" {
			if t.Received > 0 {
				return Ignore
			}
			return Rethrow
		}
		if t.WriteType == "UNLOGGED_BATCH" {
			return Retry
		}
		return Rethrow
	case *RequestErrReadTimeout:
		return Retry
	default:
		return RetryNextHost
	}
}

func (e *ExponentialBackoffRetryPolicy) napTime(attempts int) time.Duration {
	return getExponentialTime(e.Min, e.Max, attempts)
}

type HostStateNotifier interface {
	AddHost(host *HostInfo)
	RemoveHost(host *HostInfo)
	HostUp(host *HostInfo)
	HostDown(host *HostInfo)
}

type KeyspaceUpdateEvent struct {
	Keyspace string
	Change   string
}

// HostSelectionPolicy is an interface for selecting
// the most appropriate host to execute a given query.
type HostSelectionPolicy interface {
	HostStateNotifier
	SetPartitioner
	KeyspaceChanged(KeyspaceUpdateEvent)
	Init(*Session)
	IsLocal(host *HostInfo) bool
	//Pick returns an iteration function over selected hosts
	Pick(ExecutableQuery) NextHost
}

// SelectedHost is an interface returned when picking a host from a host
// selection policy.
type SelectedHost interface {
	Info() *HostInfo
	Mark(error)
}

type selectedHost HostInfo

func (host *selectedHost) Info() *HostInfo {
	return (*HostInfo)(host)
}

func (host *selectedHost) Mark(err error) {}

// NextHost is an iteration function over picked hosts
type NextHost func() SelectedHost

// RoundRobinHostPolicy is a round-robin load balancing policy, where each host
// is tried sequentially for each query.
func RoundRobinHostPolicy() HostSelectionPolicy {
	return &roundRobinHostPolicy{}
}

type roundRobinHostPolicy struct {
	hosts cowHostList
}

func (r *roundRobinHostPolicy) IsLocal(*HostInfo) bool              { return true }
func (r *roundRobinHostPolicy) KeyspaceChanged(KeyspaceUpdateEvent) {}
func (r *roundRobinHostPolicy) SetPartitioner(partitioner string)   {}
func (r *roundRobinHostPolicy) Init(*Session)                       {}

func (r *roundRobinHostPolicy) Pick(qry ExecutableQuery) NextHost {
	src := r.hosts.get()
	hosts := make([]*HostInfo, len(src))
	copy(hosts, src)

	rand := rand.New(randSource())
	rand.Shuffle(len(hosts), func(i, j int) {
		hosts[i], hosts[j] = hosts[j], hosts[i]
	})

	return roundRobbin(hosts)
}

func (r *roundRobinHostPolicy) AddHost(host *HostInfo) {
	r.hosts.add(host)
}

func (r *roundRobinHostPolicy) RemoveHost(host *HostInfo) {
	r.hosts.remove(host.ConnectAddress())
}

func (r *roundRobinHostPolicy) HostUp(host *HostInfo) {
	r.AddHost(host)
}

func (r *roundRobinHostPolicy) HostDown(host *HostInfo) {
	r.RemoveHost(host)
}

func ShuffleReplicas() func(*tokenAwareHostPolicy) {
	return func(t *tokenAwareHostPolicy) {
		t.shuffleReplicas = true
	}
}

// NonLocalReplicasFallback enables fallback to replicas that are not considered local.
//
// TokenAwareHostPolicy used with DCAwareHostPolicy fallback first selects replicas by partition key in local DC, then
// falls back to other nodes in the local DC. Enabling NonLocalReplicasFallback causes TokenAwareHostPolicy
// to first select replicas by partition key in local DC, then replicas by partition key in remote DCs and fall back
// to other nodes in local DC.
func NonLocalReplicasFallback() func(policy *tokenAwareHostPolicy) {
	return func(t *tokenAwareHostPolicy) {
		t.nonLocalReplicasFallback = true
	}
}

// TokenAwareHostPolicy is a token aware host selection policy, where hosts are
// selected based on the partition key, so queries are sent to the host which
// owns the partition. Fallback is used when routing information is not available.
func TokenAwareHostPolicy(fallback HostSelectionPolicy, opts ...func(*tokenAwareHostPolicy)) HostSelectionPolicy {
	p := &tokenAwareHostPolicy{fallback: fallback}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// clusterMeta holds metadata about cluster topology.
// It is used inside atomic.Value and shallow copies are used when replacing it,
// so fields should not be modified in-place. Instead, to modify a field a copy of the field should be made
// and the pointer in clusterMeta updated to point to the new value.
type clusterMeta struct {
	// replicas is map[keyspace]map[token]hosts
	replicas  map[string]tokenRingReplicas
	tokenRing *tokenRing
}

type tokenAwareHostPolicy struct {
	fallback            HostSelectionPolicy
	getKeyspaceMetadata func(keyspace string) (*KeyspaceMetadata, error)
	getKeyspaceName     func() string

	shuffleReplicas          bool
	nonLocalReplicasFallback bool

	// mu protects writes to hosts, partitioner, metadata.
	// reads can be unlocked as long as they are not used for updating state later.
	mu          sync.Mutex
	hosts       cowHostList
	partitioner string
	metadata    atomic.Value // *clusterMeta
}

func (t *tokenAwareHostPolicy) Init(s *Session) {
	t.getKeyspaceMetadata = s.KeyspaceMetadata
	t.getKeyspaceName = func() string { return s.cfg.Keyspace }
}

func (t *tokenAwareHostPolicy) IsLocal(host *HostInfo) bool {
	return t.fallback.IsLocal(host)
}

func (t *tokenAwareHostPolicy) KeyspaceChanged(update KeyspaceUpdateEvent) {
	t.mu.Lock()
	defer t.mu.Unlock()
	meta := t.getMetadataForUpdate()
	t.updateReplicas(meta, update.Keyspace)
	t.metadata.Store(meta)
}

// updateReplicas updates replicas in clusterMeta.
// It must be called with t.mu mutex locked.
// meta must not be nil and it's replicas field will be updated.
func (t *tokenAwareHostPolicy) updateReplicas(meta *clusterMeta, keyspace string) {
	newReplicas := make(map[string]tokenRingReplicas, len(meta.replicas))

	ks, err := t.getKeyspaceMetadata(keyspace)
	if err == nil {
		strat := getStrategy(ks)
		if strat != nil {
			if meta != nil && meta.tokenRing != nil {
				newReplicas[keyspace] = strat.replicaMap(meta.tokenRing)
			}
		}
	}

	for ks, replicas := range meta.replicas {
		if ks != keyspace {
			newReplicas[ks] = replicas
		}
	}

	meta.replicas = newReplicas
}

func (t *tokenAwareHostPolicy) SetPartitioner(partitioner string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.partitioner != partitioner {
		t.fallback.SetPartitioner(partitioner)
		t.partitioner = partitioner
		meta := t.getMetadataForUpdate()
		meta.resetTokenRing(t.partitioner, t.hosts.get())
		t.updateReplicas(meta, t.getKeyspaceName())
		t.metadata.Store(meta)
	}
}

func (t *tokenAwareHostPolicy) AddHost(host *HostInfo) {
	t.mu.Lock()
	if t.hosts.add(host) {
		meta := t.getMetadataForUpdate()
		meta.resetTokenRing(t.partitioner, t.hosts.get())
		t.updateReplicas(meta, t.getKeyspaceName())
		t.metadata.Store(meta)
	}
	t.mu.Unlock()

	t.fallback.AddHost(host)
}

func (t *tokenAwareHostPolicy) AddHosts(hosts []*HostInfo) {
	t.mu.Lock()

	for _, host := range hosts {
		t.hosts.add(host)
	}

	meta := t.getMetadataForUpdate()
	meta.resetTokenRing(t.partitioner, t.hosts.get())
	t.updateReplicas(meta, t.getKeyspaceName())
	t.metadata.Store(meta)

	t.mu.Unlock()

	for _, host := range hosts {
		t.fallback.AddHost(host)
	}
}

func (t *tokenAwareHostPolicy) RemoveHost(host *HostInfo) {
	t.mu.Lock()
	if t.hosts.remove(host.ConnectAddress()) {
		meta := t.getMetadataForUpdate()
		meta.resetTokenRing(t.partitioner, t.hosts.get())
		t.updateReplicas(meta, t.getKeyspaceName())
		t.metadata.Store(meta)
	}
	t.mu.Unlock()

	t.fallback.RemoveHost(host)
}

func (t *tokenAwareHostPolicy) HostUp(host *HostInfo) {
	t.fallback.HostUp(host)
}

func (t *tokenAwareHostPolicy) HostDown(host *HostInfo) {
	t.fallback.HostDown(host)
}

// getMetadataReadOnly returns current cluster metadata.
// Metadata uses copy on write, so the returned value should be only used for reading.
// To obtain a copy that could be updated, use getMetadataForUpdate instead.
func (t *tokenAwareHostPolicy) getMetadataReadOnly() *clusterMeta {
	meta, _ := t.metadata.Load().(*clusterMeta)
	return meta
}

// getMetadataForUpdate returns clusterMeta suitable for updating.
// It is a SHALLOW copy of current metadata in case it was already set or new empty clusterMeta otherwise.
// This function should be called with t.mu mutex locked and the mutex should not be released before
// storing the new metadata.
func (t *tokenAwareHostPolicy) getMetadataForUpdate() *clusterMeta {
	metaReadOnly := t.getMetadataReadOnly()
	meta := new(clusterMeta)
	if metaReadOnly != nil {
		*meta = *metaReadOnly
	}
	return meta
}

// resetTokenRing creates a new tokenRing.
// It must be called with t.mu locked.
func (m *clusterMeta) resetTokenRing(partitioner string, hosts []*HostInfo) {
	if partitioner == "" {
		// partitioner not yet set
		return
	}

	// create a new token ring
	tokenRing, err := newTokenRing(partitioner, hosts)
	if err != nil {
		Logger.Printf("Unable to update the token ring due to error: %s", err)
		return
	}

	// replace the token ring
	m.tokenRing = tokenRing
}

func (t *tokenAwareHostPolicy) Pick(qry ExecutableQuery) NextHost {
	if qry == nil {
		return t.fallback.Pick(qry)
	}

	routingKey, err := qry.GetRoutingKey()
	if err != nil {
		return t.fallback.Pick(qry)
	} else if routingKey == nil {
		return t.fallback.Pick(qry)
	}

	meta := t.getMetadataReadOnly()
	if meta == nil || meta.tokenRing == nil {
		return t.fallback.Pick(qry)
	}

	token := meta.tokenRing.partitioner.Hash(routingKey)
	ht := meta.replicas[qry.Keyspace()].replicasFor(token)

	var replicas []*HostInfo
	if ht == nil {
		host, _ := meta.tokenRing.GetHostForToken(token)
		replicas = []*HostInfo{host}
	} else if t.shuffleReplicas {
		replicas = shuffleHosts(replicas)
	} else {
		replicas = ht.hosts
	}

	var (
		fallbackIter NextHost
		i, j         int
		remote       []*HostInfo
	)

	used := make(map[*HostInfo]bool, len(replicas))
	return func() SelectedHost {
		for i < len(replicas) {
			h := replicas[i]
			i++

			if !t.fallback.IsLocal(h) {
				remote = append(remote, h)
				continue
			}

			if h.IsUp() {
				used[h] = true
				return (*selectedHost)(h)
			}
		}

		if t.nonLocalReplicasFallback {
			for j < len(remote) {
				h := remote[j]
				j++

				if h.IsUp() {
					used[h] = true
					return (*selectedHost)(h)
				}
			}
		}

		if fallbackIter == nil {
			// fallback
			fallbackIter = t.fallback.Pick(qry)
		}

		// filter the token aware selected hosts from the fallback hosts
		for fallbackHost := fallbackIter(); fallbackHost != nil; fallbackHost = fallbackIter() {
			if !used[fallbackHost.Info()] {
				used[fallbackHost.Info()] = true
				return fallbackHost
			}
		}

		return nil
	}
}

// HostPoolHostPolicy is a host policy which uses the bitly/go-hostpool library
// to distribute queries between hosts and prevent sending queries to
// unresponsive hosts. When creating the host pool that is passed to the policy
// use an empty slice of hosts as the hostpool will be populated later by gocql.
// See below for examples of usage:
//
//     // Create host selection policy using a simple host pool
//     cluster.PoolConfig.HostSelectionPolicy = HostPoolHostPolicy(hostpool.New(nil))
//
//     // Create host selection policy using an epsilon greedy pool
//     cluster.PoolConfig.HostSelectionPolicy = HostPoolHostPolicy(
//         hostpool.NewEpsilonGreedy(nil, 0, &hostpool.LinearEpsilonValueCalculator{}),
//     )
//
func HostPoolHostPolicy(hp hostpool.HostPool) HostSelectionPolicy {
	return &hostPoolHostPolicy{hostMap: map[string]*HostInfo{}, hp: hp}
}

type hostPoolHostPolicy struct {
	hp      hostpool.HostPool
	mu      sync.RWMutex
	hostMap map[string]*HostInfo
}

func (r *hostPoolHostPolicy) Init(*Session)                       {}
func (r *hostPoolHostPolicy) KeyspaceChanged(KeyspaceUpdateEvent) {}
func (r *hostPoolHostPolicy) SetPartitioner(string)               {}
func (r *hostPoolHostPolicy) IsLocal(*HostInfo) bool              { return true }

func (r *hostPoolHostPolicy) SetHosts(hosts []*HostInfo) {
	peers := make([]string, len(hosts))
	hostMap := make(map[string]*HostInfo, len(hosts))

	for i, host := range hosts {
		ip := host.ConnectAddress().String()
		peers[i] = ip
		hostMap[ip] = host
	}

	r.mu.Lock()
	r.hp.SetHosts(peers)
	r.hostMap = hostMap
	r.mu.Unlock()
}

func (r *hostPoolHostPolicy) AddHost(host *HostInfo) {
	ip := host.ConnectAddress().String()

	r.mu.Lock()
	defer r.mu.Unlock()

	// If the host addr is present and isn't nil return
	if h, ok := r.hostMap[ip]; ok && h != nil {
		return
	}
	// otherwise, add the host to the map
	r.hostMap[ip] = host
	// and construct a new peer list to give to the HostPool
	hosts := make([]string, 0, len(r.hostMap))
	for addr := range r.hostMap {
		hosts = append(hosts, addr)
	}

	r.hp.SetHosts(hosts)
}

func (r *hostPoolHostPolicy) RemoveHost(host *HostInfo) {
	ip := host.ConnectAddress().String()

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.hostMap[ip]; !ok {
		return
	}

	delete(r.hostMap, ip)
	hosts := make([]string, 0, len(r.hostMap))
	for _, host := range r.hostMap {
		hosts = append(hosts, host.ConnectAddress().String())
	}

	r.hp.SetHosts(hosts)
}

func (r *hostPoolHostPolicy) HostUp(host *HostInfo) {
	r.AddHost(host)
}

func (r *hostPoolHostPolicy) HostDown(host *HostInfo) {
	r.RemoveHost(host)
}

func (r *hostPoolHostPolicy) Pick(qry ExecutableQuery) NextHost {
	return func() SelectedHost {
		r.mu.RLock()
		defer r.mu.RUnlock()

		if len(r.hostMap) == 0 {
			return nil
		}

		hostR := r.hp.Get()
		host, ok := r.hostMap[hostR.Host()]
		if !ok {
			return nil
		}

		return selectedHostPoolHost{
			policy: r,
			info:   host,
			hostR:  hostR,
		}
	}
}

// selectedHostPoolHost is a host returned by the hostPoolHostPolicy and
// implements the SelectedHost interface
type selectedHostPoolHost struct {
	policy *hostPoolHostPolicy
	info   *HostInfo
	hostR  hostpool.HostPoolResponse
}

func (host selectedHostPoolHost) Info() *HostInfo {
	return host.info
}

func (host selectedHostPoolHost) Mark(err error) {
	ip := host.info.ConnectAddress().String()

	host.policy.mu.RLock()
	defer host.policy.mu.RUnlock()

	if _, ok := host.policy.hostMap[ip]; !ok {
		// host was removed between pick and mark
		return
	}

	host.hostR.Mark(err)
}

type dcAwareRR struct {
	local       string
	localHosts  cowHostList
	remoteHosts cowHostList
}

// DCAwareRoundRobinPolicy is a host selection policies which will prioritize and
// return hosts which are in the local datacentre before returning hosts in all
// other datercentres
func DCAwareRoundRobinPolicy(localDC string) HostSelectionPolicy {
	return &dcAwareRR{local: localDC}
}

func (d *dcAwareRR) Init(*Session)                       {}
func (d *dcAwareRR) KeyspaceChanged(KeyspaceUpdateEvent) {}
func (d *dcAwareRR) SetPartitioner(p string)             {}

func (d *dcAwareRR) IsLocal(host *HostInfo) bool {
	return host.DataCenter() == d.local
}

func (d *dcAwareRR) AddHost(host *HostInfo) {
	if d.IsLocal(host) {
		d.localHosts.add(host)
	} else {
		d.remoteHosts.add(host)
	}
}

func (d *dcAwareRR) RemoveHost(host *HostInfo) {
	if d.IsLocal(host) {
		d.localHosts.remove(host.ConnectAddress())
	} else {
		d.remoteHosts.remove(host.ConnectAddress())
	}
}

func (d *dcAwareRR) HostUp(host *HostInfo)   { d.AddHost(host) }
func (d *dcAwareRR) HostDown(host *HostInfo) { d.RemoveHost(host) }

var randSeed int64

func init() {
	p := make([]byte, 8)
	if _, err := crand.Read(p); err != nil {
		panic(err)
	}
	randSeed = int64(binary.BigEndian.Uint64(p))
}

func randSource() rand.Source {
	return rand.NewSource(atomic.AddInt64(&randSeed, 1))
}

func roundRobbin(hosts []*HostInfo) NextHost {
	var i int
	return func() SelectedHost {
		for i < len(hosts) {
			h := hosts[i]
			i++

			if h.IsUp() {
				return (*selectedHost)(h)
			}
		}

		return nil
	}
}

func (d *dcAwareRR) Pick(q ExecutableQuery) NextHost {
	local := d.localHosts.get()
	remote := d.remoteHosts.get()

	hosts := make([]*HostInfo, len(local)+len(remote))
	n := copy(hosts, local)
	copy(hosts[n:], remote)

	// TODO: use random chose-2 but that will require plumbing information
	// about connection/host load to here
	r := rand.New(randSource())
	for _, l := range [][]*HostInfo{hosts[:len(local)], hosts[len(local):]} {
		r.Shuffle(len(l), func(i, j int) {
			l[i], l[j] = l[j], l[i]
		})
	}

	return roundRobbin(hosts)
}

// ConvictionPolicy interface is used by gocql to determine if a host should be
// marked as DOWN based on the error and host info
type ConvictionPolicy interface {
	// Implementations should return `true` if the host should be convicted, `false` otherwise.
	AddFailure(error error, host *HostInfo) bool
	//Implementations should clear out any convictions or state regarding the host.
	Reset(host *HostInfo)
}

// SimpleConvictionPolicy implements a ConvictionPolicy which convicts all hosts
// regardless of error
type SimpleConvictionPolicy struct {
}

func (e *SimpleConvictionPolicy) AddFailure(error error, host *HostInfo) bool {
	return true
}

func (e *SimpleConvictionPolicy) Reset(host *HostInfo) {}

// ReconnectionPolicy interface is used by gocql to determine if reconnection
// can be attempted after connection error. The interface allows gocql users
// to implement their own logic to determine how to attempt reconnection.
//
type ReconnectionPolicy interface {
	GetInterval(currentRetry int) time.Duration
	GetMaxRetries() int
}

// ConstantReconnectionPolicy has simple logic for returning a fixed reconnection interval.
//
// Examples of usage:
//
//     cluster.ReconnectionPolicy = &gocql.ConstantReconnectionPolicy{MaxRetries: 10, Interval: 8 * time.Second}
//
type ConstantReconnectionPolicy struct {
	MaxRetries int
	Interval   time.Duration
}

func (c *ConstantReconnectionPolicy) GetInterval(currentRetry int) time.Duration {
	return c.Interval
}

func (c *ConstantReconnectionPolicy) GetMaxRetries() int {
	return c.MaxRetries
}

// ExponentialReconnectionPolicy returns a growing reconnection interval.
type ExponentialReconnectionPolicy struct {
	MaxRetries      int
	InitialInterval time.Duration
}

func (e *ExponentialReconnectionPolicy) GetInterval(currentRetry int) time.Duration {
	return getExponentialTime(e.InitialInterval, math.MaxInt16*time.Second, e.GetMaxRetries())
}

func (e *ExponentialReconnectionPolicy) GetMaxRetries() int {
	return e.MaxRetries
}

type SpeculativeExecutionPolicy interface {
	Attempts() int
	Delay() time.Duration
}

type NonSpeculativeExecution struct{}

func (sp NonSpeculativeExecution) Attempts() int        { return 0 } // No additional attempts
func (sp NonSpeculativeExecution) Delay() time.Duration { return 1 } // The delay. Must be positive to be used in a ticker.

type SimpleSpeculativeExecution struct {
	NumAttempts  int
	TimeoutDelay time.Duration
}

func (sp *SimpleSpeculativeExecution) Attempts() int        { return sp.NumAttempts }
func (sp *SimpleSpeculativeExecution) Delay() time.Duration { return sp.TimeoutDelay }
