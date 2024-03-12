/*
Copyright 2021 The Dapr Authors
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

// Package placement is an implementation of Consistent Hashing and
// Consistent Hashing With Bounded Loads.
//
// https://en.wikipedia.org/wiki/Consistent_hashing
//
// https://research.googleblog.com/2017/04/consistent-hashing-with-bounded-loads.html
//
// https://github.com/lafikl/consistent/blob/master/consistent.go
package hashing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/blake2b"
)

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
	hosts             map[uint64]string
	sortedSet         []uint64
	loadMap           map[string]*Host
	totalLoad         int64
	replicationFactor int64

	sync.RWMutex
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
func NewConsistentHash(replicationFactory int64) *Consistent {
	return &Consistent{
		hosts:             map[uint64]string{},
		sortedSet:         []uint64{},
		loadMap:           map[string]*Host{},
		replicationFactor: replicationFactory,
	}
}

// NewFromExisting creates a new consistent hash from existing values.
func NewFromExisting(loadMap map[string]*Host, replicationFactor int64, virtualNodesCache *VirtualNodesCache) *Consistent {
	newHash := &Consistent{
		hosts:             map[uint64]string{},
		sortedSet:         []uint64{},
		loadMap:           loadMap,
		replicationFactor: replicationFactor,
	}

	for hostName := range loadMap {
		hashes := virtualNodesCache.GetHashes(replicationFactor, hostName)
		for _, h := range hashes {
			newHash.hosts[h] = hostName
		}
		newHash.sortedSet = append(newHash.sortedSet, hashes...)
	}

	// sort hashes in ascending order
	sort.Slice(newHash.sortedSet, func(i int, j int) bool {
		return newHash.sortedSet[i] < newHash.sortedSet[j]
	})

	return newHash
}

// VirtualNodesCache data example:
//
//	100 -> (the replication factor)
//		"192.168.1.89:62362" -> ["10056481384176189962", "10100244799470048543"...] (100 elements)
//		"192.168.1.89:62362" -> ["10056481384176189962", "10100244799470048543"...]
//	200 ->
//		"192.168.1.89:62362" -> ["10056481384176189962", "10100244799470048543"...] (200 elements)
//		"192.168.1.89:62362" -> ["10056481384176189962", "10100244799470048543"...]
type VirtualNodesCache struct {
	sync.RWMutex
	data map[int64]*hashMap
}

// hashMap represents a mapping of IP addresses to their hashes.
type hashMap struct {
	hashes map[string][]uint64
}

func newHashMap() *hashMap {
	return &hashMap{
		hashes: make(map[string][]uint64),
	}
}

func NewVirtualNodesCache() *VirtualNodesCache {
	return &VirtualNodesCache{
		data: make(map[int64]*hashMap),
	}
}

// GetHashes retrieves the hashes for the given replication factor and IP address.
func (hc *VirtualNodesCache) GetHashes(replicationFactor int64, host string) []uint64 {
	hc.RLock()
	if hashMap, exists := hc.data[replicationFactor]; exists {
		if hashes, found := hashMap.hashes[host]; found {
			hc.RUnlock()
			return hashes
		}
	}
	hc.RUnlock()

	return hc.setHashes(replicationFactor, host)
}

// SetHashes sets the hashes for the given replication factor and IP address.
func (hc *VirtualNodesCache) setHashes(replicationFactor int64, host string) []uint64 {
	hc.Lock()
	defer hc.Unlock()

	// We have to check once again if the hash map exists, because by this point
	// we already released the previous lock (in getHashes) and another goroutine might have
	// created the hash map in the meantime.
	if hashMap, exists := hc.data[replicationFactor]; exists {
		if hashes, found := hashMap.hashes[host]; found {
			return hashes
		}
	}

	hashMap := newHashMap()
	hashMap.hashes[host] = make([]uint64, replicationFactor)

	for i := 0; i < int(replicationFactor); i++ {
		hashMap.hashes[host][i] = hash(host + strconv.Itoa(i))
	}

	hc.data[replicationFactor] = hashMap

	return hashMap.hashes[host]
}

// NewFromExistingWithVirtNodes creates a new consistent hash from existing values with vnodes
// It's a legacy function needed for backwards compatibility (daprd >= 1.13 with placement < 1.13)
// TODO: @elena in v1.15 remove this function
func NewFromExistingWithVirtNodes(hosts map[uint64]string, sortedSet []uint64, loadMap map[string]*Host) *Consistent {
	return &Consistent{
		hosts:     hosts,
		sortedSet: sortedSet,
		loadMap:   loadMap,
	}
}

// ReadInternals returns the internal data structure of the consistent hash.
func (c *Consistent) ReadInternals(reader func(map[uint64]string, []uint64, map[string]*Host, int64)) {
	c.RLock()
	defer c.RUnlock()

	reader(c.hosts, c.sortedSet, c.loadMap, c.totalLoad)
}

// Add adds a host with port to the table.
func (c *Consistent) Add(host, id string, port int64) bool {
	c.Lock()
	defer c.Unlock()

	if _, ok := c.loadMap[host]; ok {
		return true
	}

	c.loadMap[host] = &Host{Name: host, AppID: id, Load: 0, Port: port}

	// TODO: @elena in v1.15
	// The optimisation of not disseminating vnodes with the placement table was introduced
	// in 1.13, and the API level was increased to 20, but we still have to support sidecars
	// running on 1.12 with placement services on 1.13. That's why we are keeping the
	// vhosts in the store in v1.13.
	// This should be removed in 1.15.
	// --Start remove--
	for i := 0; i < int(c.replicationFactor); i++ {
		h := hash(host + strconv.Itoa(i))
		c.hosts[h] = host
		c.sortedSet = append(c.sortedSet, h)
	}
	// sort hashes in ascending order
	sort.Slice(c.sortedSet, func(i int, j int) bool {
		return c.sortedSet[i] < c.sortedSet[j]
	})
	// --End remove--

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

	h := hash(key)
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
func (c *Consistent) GetLeast(key string) (string, error) {
	c.RLock()
	defer c.RUnlock()

	if len(c.hosts) == 0 {
		return "", ErrNoHosts
	}

	h := hash(key)
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

	for i := 0; i < int(c.replicationFactor); i++ {
		h := hash(host + strconv.Itoa(i))
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

func (c *Consistent) VirtualNodes() map[uint64]string {
	c.RLock()
	defer c.RUnlock()

	virtualNodes := make(map[uint64]string, len(c.hosts))
	for vn, h := range c.hosts {
		virtualNodes[vn] = h
	}
	return virtualNodes
}

func (c *Consistent) SortedSet() (sortedSet []uint64) {
	c.RLock()
	defer c.RUnlock()

	return c.sortedSet
}

func hash(key string) uint64 {
	out := blake2b.Sum512([]byte(key))
	return binary.LittleEndian.Uint64(out[:])
}
