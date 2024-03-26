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

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	loaderdisk "github.com/dapr/dapr/pkg/internal/loader/disk"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader"
	"github.com/dapr/dapr/pkg/runtime/hotreload/loader/store"
	"github.com/dapr/kit/concurrency"
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
	fs        *fswatcher.FSWatcher
	updateCh  chan<- struct{}
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

	updateCh := make(chan struct{})

	return &disk{
		fs: fs,
		component: newResource[componentsapi.Component](
			loaderdisk.New[componentsapi.Component](opts.Dirs...),
			store.NewComponent(opts.ComponentStore),
			updateCh,
		),
		updateCh: updateCh,
	}, nil
}

func (d *disk) Run(ctx context.Context) error {
	return concurrency.NewRunnerManager(
		d.component.start,
		func(ctx context.Context) error {
			return d.fs.Run(ctx, d.updateCh)
		},
	).Run(ctx)
}

func (d *disk) Components() loader.Loader[componentsapi.Component] {
	return d.component
}
