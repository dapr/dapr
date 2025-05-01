/*
Copyright 2025 The Dapr Authors
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

package input

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dapr/components-contrib/bindings"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.processor.binding.input")

type Options struct {
	Name    string
	Binding bindings.InputBinding
	Handler func(context.Context, string, []byte, map[string]string) ([]byte, error)
}

type Input struct {
	name    string
	binding bindings.InputBinding
	handler func(context.Context, string, []byte, map[string]string) ([]byte, error)

	cancel   func()
	closed   atomic.Bool
	wg       sync.WaitGroup
	inflight atomic.Int64
}

func Run(opts Options) (*Input, error) {
	ctx, cancel := context.WithCancel(context.Background())

	i := &Input{
		name:    opts.Name,
		binding: opts.Binding,
		handler: opts.Handler,
		cancel:  cancel,
	}

	return i, i.read(ctx)
}

func (i *Input) Stop() {
	i.closed.Store(true)
	inflight := i.inflight.Load() > 0

	i.wg.Wait()

	// If there were in-flight requests then wait some time for the result to be
	// sent to the binding. This is because the message result context is
	// disparate.
	if inflight {
		time.Sleep(time.Millisecond * 400)
	}
	i.cancel()
}

func (i *Input) read(ctx context.Context) error {
	return i.binding.Read(ctx, func(ctx context.Context, resp *bindings.ReadResponse) ([]byte, error) {
		i.wg.Add(1)
		i.inflight.Add(1)
		defer func() {
			i.wg.Done()
			i.inflight.Add(-1)
		}()

		if i.closed.Load() {
			return nil, errors.New("input binding is closed")
		}

		if resp == nil {
			return nil, nil
		}

		start := time.Now()
		b, err := i.handler(ctx, i.name, resp.Data, resp.Metadata)
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.InputBindingEvent(context.Background(), i.name, err == nil, elapsed)

		if err != nil {
			log.Debugf("error from app consumer for binding [%s]: %s", i.name, err)
			return nil, err
		}

		return b, nil
	})
}
