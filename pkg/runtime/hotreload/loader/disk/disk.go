/*
Copyright 2023 The Dapr Authors
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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	loadercompstore "github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/events/batcher"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.hotreload.loader.disk")

type Options struct {
	Dirs           []string
	ComponentStore *compstore.ComponentStore
}

type disk struct {
	component *resource[componentsapi.Component]

	wg      sync.WaitGroup
	closeCh chan struct{}
	closed  atomic.Bool
}

func New(ctx context.Context, opts Options) (loader.Interface, error) {
	log.Infof("Watching directories: [%s]", strings.Join(opts.Dirs, ", "))

	fs, err := fswatcher.New(fswatcher.Options{
		Targets:  opts.Dirs,
		Interval: ptr.Of(time.Millisecond * 200),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	batcher := batcher.New[int](0)
	eventCh := make(chan struct{})

	d := &disk{
		closeCh:   make(chan struct{}),
		component: newResource[componentsapi.Component](opts, batcher, loadercompstore.NewComponent(opts.ComponentStore)),
	}

	ctx, cancel := context.WithCancel(ctx)

	d.wg.Add(2)
	go func() {
		if err := fs.Run(ctx, eventCh); err != nil {
			log.Errorf("Error watching directories: %s", err)
		}
		d.wg.Done()
	}()

	go func() {
		defer d.wg.Done()
		defer batcher.Close()
		defer cancel()
		var i int
		for {
			select {
			case <-d.closeCh:
				return
			case <-ctx.Done():
				return

			case <-eventCh:
				// Use a separate: index every batch to prevent deduplicates of separate
				// file updates happening at the same time.
				i++
				batcher.Batch(i)
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

	return errors.Join(errs...)
}

func (d *disk) Components() loader.Loader[componentsapi.Component] {
	return d.component
}
