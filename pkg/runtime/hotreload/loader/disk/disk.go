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

package disk

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/compstore"
	"github.com/dapr/kit/batcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.hotreload.loader.disk")

type Options struct {
	Dirs           []string
	ComponentStore *compstore.ComponentStore
}

type disk struct {
	component *generic[compapi.Component]
	endpoint  *generic[httpendapi.HTTPEndpoint]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

func New(opts Options) (loader.Interface, error) {
	log.Infof("Watching directories: [%s]", strings.Join(opts.Dirs, ", "))

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	for _, dir := range opts.Dirs {
		if err = watcher.Add(dir); err != nil {
			watcher.Close()
			return nil, err
		}
	}

	batcher := batcher.New[string](time.Millisecond * 300)

	d := &disk{
		closeCh:   make(chan struct{}),
		component: newGeneric[compapi.Component](opts, batcher, loadercompstore.NewComponent(opts.ComponentStore)),
		endpoint:  newGeneric[httpendapi.HTTPEndpoint](opts, batcher, loadercompstore.NewEndpoint(opts.ComponentStore)),
	}

	d.wg.Add(1)
	defer d.wg.Done()
	go func() {
		for {
			select {
			case <-d.closeCh:
				return
			// Often the case, writes to files are not atomic and involve multiple
			// file system events. We want to hold off on sending events until we are
			// sure that the file has been written to completion. We do this by
			// waiting for a period of time after the last event has been received
			// for a file name.
			case event := <-watcher.Events:
				batcher.Batch(event.Name)
			}
		}
	}()

	return d, nil
}

func (d *disk) Close() error {
	defer d.wg.Wait()
	if d.closed.CompareAndSwap(false, true) {
		close(d.closeCh)
	}

	var errs []error
	if err := d.component.close(); err != nil {
		errs = append(errs, err)
	}

	if err := d.endpoint.close(); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

func (d *disk) Components() loader.Loader[compapi.Component] {
	return d.component
}

func (d *disk) HTTPEndpoints() loader.Loader[httpendapi.HTTPEndpoint] {
	return d.endpoint
}
