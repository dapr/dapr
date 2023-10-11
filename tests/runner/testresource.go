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

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Disposable is an interface representing the disposable test resources.
type Disposable interface {
	Name() string
	Init(ctx context.Context) error
	Dispose(wait bool) error
}

// TestResources holds initial resources and active resources.
type TestResources struct {
	resources           []Disposable
	resourcesLock       sync.Mutex
	activeResources     []Disposable
	activeResourcesLock sync.Mutex
	ctx                 context.Context
	cancel              context.CancelFunc
}

// Add adds Disposable resource to resources queue.
func (r *TestResources) Add(dr Disposable) {
	r.resourcesLock.Lock()
	defer r.resourcesLock.Unlock()
	r.resources = append(r.resources, dr)
}

// dequeueResource dequeues Disposable resource from resources queue.
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

// pushActiveResource pushes Disposable resource to ActiveResource stack.
func (r *TestResources) pushActiveResource(dr Disposable) {
	r.activeResourcesLock.Lock()
	defer r.activeResourcesLock.Unlock()
	r.activeResources = append(r.activeResources, dr)
}

// popActiveResource pops Disposable resource from ActiveResource stack.
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

// FindActiveResource finds active resource by resource name.
func (r *TestResources) FindActiveResource(name string) Disposable {
	for _, res := range r.activeResources {
		if res.Name() == name {
			return res
		}
	}

	return nil
}

// Setup initializes the resources by calling Setup.
func (r *TestResources) setup() error {
	r.ctx, r.cancel = context.WithCancel(context.Background())

	resourceCount := 0
	errs := make(chan error)
	for {
		dr := r.dequeueResource()
		if dr == nil {
			break
		}

		resourceCount++
		go func() {
			err := dr.Init(r.ctx)
			r.pushActiveResource(dr)
			errs <- err
		}()
	}

	allErrs := make([]error, 0)
	for i := 0; i < resourceCount; i++ {
		err := <-errs
		if err != nil {
			allErrs = append(allErrs, err)
		}
	}

	return errors.Join(allErrs...)
}

// TearDown initializes the resources by calling Dispose.
func (r *TestResources) tearDown() error {
	resourceCount := 0
	errs := make(chan error)
	for {
		dr := r.popActiveResource()
		if dr == nil {
			break
		}

		resourceCount++
		go func() {
			err := dr.Dispose(false)
			if err != nil {
				err = fmt.Errorf("failed to tear down %s. got: %w", dr.Name(), err)
			}
			errs <- err
		}()
	}

	allErrs := make([]error, 0)
	for i := 0; i < resourceCount; i++ {
		err := <-errs
		if err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			allErrs = append(allErrs, err)
		}
	}

	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return errors.Join(allErrs...)
}
