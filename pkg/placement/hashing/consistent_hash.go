// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Package placement is an implementation of Consistent Hashing and
// Consistent Hashing With Bounded Loads.
//
// https://en.wikipedia.org/wiki/Consistent_hashing
//
// https://research.googleblog.com/2017/04/consistent-hashing-with-bounded-loads.html
//
// https://github.com/lafikl/consistent/blob/master/consistent.go
//
package hashing

import (
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"

	blake2b "github.com/minio/blake2b-simd"
	"github.com/pkg/errors"
)

var replicationFactor int

// ErrNoHosts is an error for no hosts.
var ErrNoHosts = errors.New("no hosts added")

// ConsistentHashTables is a table holding a map of consistent hashes with a given version.
type ConsistentHashTables struct {
	Version string
	Entries map[string]*Consistent
}

// Host represents a host of stateful entities with a given name, id, port and load.
type Host struct {
	Name  string
	Port  int64
	Load  int64
	AppID string
}

// Consistent represents a data structure for consistent hashing.
type Consistent struct {
	hosts     map[uint64]string
	sortedSet []uint64
	loadMap   map[string]*Host
	totalLoad int64

	sync.RWMutex
}

// NewPlacementTables returns new stateful placement tables with a given version.
func NewPlacementTables(version string, entries map[string]*Consistent) *ConsistentHashTables {
	return &ConsistentHashTables{
		Version: version,
		Entries: entries,
	}
}

// NewHost returns a new host.
func NewHost(name, id string, load int64, port int64) *Host {
	return &Host{
		Name:  name,
		Load:  load,
		Port:  port,
		AppID: id,
	}
}

// NewConsistentHash returns a new consistent hash.
func NewConsistentHash() *Consistent {
	return &Consistent{
		hosts:     map[uint64]string{},
		sortedSet: []uint64{},
		loadMap:   map[string]*Host{},
	}
}

// NewFromExisting creates a new consistent hash from existing values.
func NewFromExisting(hosts map[uint64]string, sortedSet []uint64, loadMap map[string]*Host) *Consistent {
	return &Consistent{
		hosts:     hosts,
		sortedSet: sortedSet,
		loadMap:   loadMap,
	}
}

// GetInternals returns the internal data structure of the consistent hash.
func (c *Consistent) GetInternals() (map[uint64]string, []uint64, map[string]*Host, int64) {
	c.RLock()
	defer c.RUnlock()

	return c.hosts, c.sortedSet, c.loadMap, c.totalLoad
}

// Add adds a host with port to the table.
func (c *Consistent) Add(host, id string, port int64) bool {
	c.Lock()
	defer c.Unlock()

	if _, ok := c.loadMap[host]; ok {
		return true
	}

	c.loadMap[host] = &Host{Name: host, AppID: id, Load: 0, Port: port}
	for i := 0; i < replicationFactor; i++ {
		h := c.hash(fmt.Sprintf("%s%d", host, i))
		c.hosts[h] = host
		c.sortedSet = append(c.sortedSet, h)
	}
	// sort hashes ascendingly
	sort.Slice(c.sortedSet, func(i int, j int) bool {
		return c.sortedSet[i] < c.sortedSet[j]
	})

	return false
}

// Get returns the host that owns `key`.
//
// As described in https://en.wikipedia.org/wiki/Consistent_hashing
//
// It returns ErrNoHosts if the ring has no hosts in it.
func (c *Consistent) Get(key string) (string, error) {
	c.RLock()
	defer c.RUnlock()

	if len(c.hosts) == 0 {
		return "", ErrNoHosts
	}

	h := c.hash(key)
	idx := c.search(h)
	return c.hosts[c.sortedSet[idx]], nil
}

// GetHost gets a host.
func (c *Consistent) GetHost(key string) (*Host, error) {
	h, err := c.Get(key)
	if err != nil {
		return nil, err
	}

	return c.loadMap[h], nil
}

// GetLeast uses Consistent Hashing With Bounded loads
//
// https://research.googleblog.com/2017/04/consistent-hashing-with-bounded-loads.html
//
// to pick the least loaded host that can serve the key
//
// It returns ErrNoHosts if the ring has no hosts in it.
//
func (c *Consistent) GetLeast(key string) (string, error) {
	c.RLock()
	defer c.RUnlock()

	if len(c.hosts) == 0 {
		return "", ErrNoHosts
	}

	h := c.hash(key)
	idx := c.search(h)

	i := idx
	for {
		host := c.hosts[c.sortedSet[i]]
		if c.loadOK(host) {
			return host, nil
		}
		i++
		if i >= len(c.hosts) {
			i = 0
		}
	}
}

func (c *Consistent) search(key uint64) int {
	idx := sort.Search(len(c.sortedSet), func(i int) bool {
		return c.sortedSet[i] >= key
	})

	if idx >= len(c.sortedSet) {
		idx = 0
	}
	return idx
}

// UpdateLoad sets the load of `host` to the given `load`.
func (c *Consistent) UpdateLoad(host string, load int64) {
	c.Lock()
	defer c.Unlock()

	if _, ok := c.loadMap[host]; !ok {
		return
	}
	c.totalLoad -= c.loadMap[host].Load
	c.loadMap[host].Load = load
	c.totalLoad += load
}

// Inc increments the load of host by 1
//
// should only be used with if you obtained a host with GetLeast.
func (c *Consistent) Inc(host string) {
	c.Lock()
	defer c.Unlock()

	atomic.AddInt64(&c.loadMap[host].Load, 1)
	atomic.AddInt64(&c.totalLoad, 1)
}

// Done decrements the load of host by 1
//
// should only be used with if you obtained a host with GetLeast.
func (c *Consistent) Done(host string) {
	c.Lock()
	defer c.Unlock()

	if _, ok := c.loadMap[host]; !ok {
		return
	}
	atomic.AddInt64(&c.loadMap[host].Load, -1)
	atomic.AddInt64(&c.totalLoad, -1)
}

// Remove deletes host from the ring.
func (c *Consistent) Remove(host string) bool {
	c.Lock()
	defer c.Unlock()

	for i := 0; i < replicationFactor; i++ {
		h := c.hash(fmt.Sprintf("%s%d", host, i))
		delete(c.hosts, h)
		c.delSlice(h)
	}
	delete(c.loadMap, host)
	return true
}

// Hosts return the list of hosts in the ring.
func (c *Consistent) Hosts() (hosts []string) {
	c.RLock()
	defer c.RUnlock()
	for k := range c.loadMap {
		hosts = append(hosts, k)
	}
	return hosts
}

// GetLoads returns the loads of all the hosts.
func (c *Consistent) GetLoads() map[string]int64 {
	loads := map[string]int64{}

	for k, v := range c.loadMap {
		loads[k] = v.Load
	}
	return loads
}

// MaxLoad returns the maximum load of the single host
// which is:
// (total_load/number_of_hosts)*1.25
// total_load = is the total number of active requests served by hosts
// for more info:
// https://research.googleblog.com/2017/04/consistent-hashing-with-bounded-loads.html
func (c *Consistent) MaxLoad() int64 {
	if c.totalLoad == 0 {
		c.totalLoad = 1
	}
	var avgLoadPerNode float64
	avgLoadPerNode = float64(c.totalLoad / int64(len(c.loadMap)))
	if avgLoadPerNode == 0 {
		avgLoadPerNode = 1
	}
	avgLoadPerNode = math.Ceil(avgLoadPerNode * 1.25)
	return int64(avgLoadPerNode)
}

func (c *Consistent) loadOK(host string) bool {
	// a safety check if someone performed c.Done more than needed
	if c.totalLoad < 0 {
		c.totalLoad = 0
	}

	var avgLoadPerNode float64
	avgLoadPerNode = float64((c.totalLoad + 1) / int64(len(c.loadMap)))
	if avgLoadPerNode == 0 {
		avgLoadPerNode = 1
	}
	avgLoadPerNode = math.Ceil(avgLoadPerNode * 1.25)

	bhost, ok := c.loadMap[host]
	if !ok {
		panic(fmt.Sprintf("given host(%s) not in loadsMap", bhost.Name))
	}

	if float64(bhost.Load)+1 <= avgLoadPerNode {
		return true
	}

	return false
}

func (c *Consistent) delSlice(val uint64) {
	idx := -1
	l := 0
	r := len(c.sortedSet) - 1
	for l <= r {
		m := (l + r) / 2
		if c.sortedSet[m] == val {
			idx = m
			break
		} else if c.sortedSet[m] < val {
			l = m + 1
		} else if c.sortedSet[m] > val {
			r = m - 1
		}
	}
	if idx != -1 {
		c.sortedSet = append(c.sortedSet[:idx], c.sortedSet[idx+1:]...)
	}
}

func (c *Consistent) hash(key string) uint64 {
	out := blake2b.Sum512([]byte(key))
	return binary.LittleEndian.Uint64(out[:])
}

// SetReplicationFactor sets the replication factor for actor placement on vnodes.
func SetReplicationFactor(factor int) {
	replicationFactor = factor
}
