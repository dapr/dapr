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
	"fmt"
	"strings"
	"time"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	loaderdisk "github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/concurrency"
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
	components    *resource[compapi.Component]
	subscriptions *resource[subapi.Subscription]
	fs            *fswatcher.FSWatcher
	batcher       *batcher.Batcher[int, struct{}]
}

func New(opts Options) (loader.Interface, error) {
	log.Infof("Watching directories: [%s]", strings.Join(opts.Dirs, ", "))

	fs, err := fswatcher.New(fswatcher.Options{
		Targets:  opts.Dirs,
		Interval: ptr.Of(time.Millisecond * 200),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	batcher := batcher.New[int, struct{}](0)

	return &disk{
		fs: fs,
		components: newResource[compapi.Component](
			resourceOptions[compapi.Component]{
				loader:  loaderdisk.NewComponents(opts.Dirs...),
				store:   store.NewComponents(opts.ComponentStore),
				batcher: batcher,
			},
		),
		subscriptions: newResource[subapi.Subscription](
			resourceOptions[subapi.Subscription]{
				loader:  loaderdisk.NewSubscriptions(opts.Dirs...),
				store:   store.NewSubscriptions(opts.ComponentStore),
				batcher: batcher,
			},
		),
		batcher: batcher,
	}, nil
}

func (d *disk) Run(ctx context.Context) error {
	eventCh := make(chan struct{})

	return concurrency.NewRunnerManager(
		d.components.run,
		d.subscriptions.run,
		func(ctx context.Context) error {
			return d.fs.Run(ctx, eventCh)
		},
		func(ctx context.Context) error {
			defer d.batcher.Close()

			var i int
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-eventCh:
					// Use a separate: index every batch to prevent deduplicates of separate
					// file updates happening at the same time.
					i++
					d.batcher.Batch(i, struct{}{})
				}
			}
		},
	).Run(ctx)
}

func (d *disk) Components() loader.Loader[compapi.Component] {
	return d.components
}

func (d *disk) Subscriptions() loader.Loader[subapi.Subscription] {
	return d.subscriptions
}
