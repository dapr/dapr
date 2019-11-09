package utils

import (
	"sync"

	log "github.com/Sirupsen/logrus"
)

// Disposable is an interface representing the disposable test resources
type Disposable interface {
	Name() string
	Setup() error
	TearDown() error
}

// TestResources holds initial resources and active resources
type TestResources struct {
	resources           []Disposable
	resourcesLock       sync.Mutex
	activeResources     []Disposable
	activeResourcesLock sync.Mutex
}

// Add addes disposable implemenation
func (r *TestResources) Add(dr Disposable) {
	r.resourcesLock.Lock()
	defer r.resourcesLock.Unlock()
	r.resources = append(r.resources, dr)
}

func (r *TestResources) dequeueResource() Disposable {
	r.resourcesLock.Lock()
	defer r.resourcesLock.Unlock()
	if len(r.resources) == 0 {
		return nil
	}
	dr := r.resources[0]
	r.resources = r.resources[1:]
	return dr
}

func (r *TestResources) pushActiveResource(dr Disposable) {
	r.activeResourcesLock.Lock()
	defer r.activeResourcesLock.Unlock()
	r.activeResources = append(r.activeResources, dr)
}

func (r *TestResources) popActiveResource() Disposable {
	r.activeResourcesLock.Lock()
	defer r.activeResourcesLock.Unlock()
	if len(r.activeResources) == 0 {
		return nil
	}
	dr := r.activeResources[len(r.activeResources)-1]
	r.activeResources = r.activeResources[:len(r.activeResources)-1]
	return dr
}

// Init initializes the resources by calling Setup
func (r *TestResources) Init() error {
	for dr := r.dequeueResource(); dr != nil; dr = r.dequeueResource() {
		err := dr.Setup()
		r.pushActiveResource(dr)
		if err != nil {
			return err
		}
	}
	return nil
}

// Cleanup initializes the resources by calling TearDown
func (r *TestResources) Cleanup() error {
	for dr := r.popActiveResource(); dr != nil; dr = r.popActiveResource() {
		err := dr.TearDown()
		if err != nil {
			log.Errorf("Failed to cleanup. Error %s", err)
		}
	}
	return nil
}

// FindActiveResource find active resource by resource name
func (r *TestResources) FindActiveResource(name string) Disposable {
	for _, res := range r.activeResources {
		if res.Name() == name {
			return res
		}
	}

	return nil
}
