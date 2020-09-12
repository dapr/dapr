// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runner

import (
	"fmt"
	"os"
	"sync"
)

// Disposable is an interface representing the disposable test resources
type Disposable interface {
	Name() string
	Init() error
	Dispose(wait bool) error
}

// TestResources holds initial resources and active resources
type TestResources struct {
	resources           []Disposable
	resourcesLock       sync.Mutex
	activeResources     []Disposable
	activeResourcesLock sync.Mutex
}

// Add adds Disposable resource to resources queue
func (r *TestResources) Add(dr Disposable) {
	r.resourcesLock.Lock()
	defer r.resourcesLock.Unlock()
	r.resources = append(r.resources, dr)
}

// dequeueResource dequeus Disposable resource from resources queue
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

// pushActiveResource pushes Disposable resource to ActiveResource stack
func (r *TestResources) pushActiveResource(dr Disposable) {
	r.activeResourcesLock.Lock()
	defer r.activeResourcesLock.Unlock()
	r.activeResources = append(r.activeResources, dr)
}

// popActiveResource pops Disposable resource from ActiveResource stack
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

// FindActiveResource finds active resource by resource name
func (r *TestResources) FindActiveResource(name string) Disposable {
	for _, res := range r.activeResources {
		if res.Name() == name {
			return res
		}
	}

	return nil
}

// Setup initializes the resources by calling Setup
func (r *TestResources) setup() error {
	for dr := r.dequeueResource(); dr != nil; dr = r.dequeueResource() {
		err := dr.Init()
		r.pushActiveResource(dr)
		if err != nil {
			return err
		}
	}
	return nil
}

// TearDown initializes the resources by calling Dispose
func (r *TestResources) tearDown() (retErr error) {
	retErr = nil
	for dr := r.popActiveResource(); dr != nil; dr = r.popActiveResource() {
		err := dr.Dispose(false)
		if err != nil {
			retErr = err
			fmt.Fprintf(os.Stderr, "Failed to tear down %s. got: %q", dr.Name(), err)
		}
	}
	return retErr
}
