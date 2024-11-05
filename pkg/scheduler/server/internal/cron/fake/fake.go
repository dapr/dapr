/*
Copyright 2024 The Dapr Authors
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

package fake

import (
	"context"

	"github.com/diagridio/go-etcd-cron/api"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

type Fake struct {
	runFn      func(context.Context) error
	clientFn   func(context.Context) (api.Interface, error)
	addWatchFn func(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) error
}

func New() *Fake {
	return &Fake{
		runFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
		clientFn: func(context.Context) (api.Interface, error) {
			return nil, nil
		},
		addWatchFn: func(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) error {
			return nil
		},
	}
}

func (f *Fake) WithRun(fn func(context.Context) error) *Fake {
	f.runFn = fn
	return f
}

func (f *Fake) WithClient(fn func(context.Context) (api.Interface, error)) *Fake {
	f.clientFn = fn
	return f
}

func (f *Fake) WithAddWatch(fn func(*schedulerv1pb.WatchJobsRequestInitial, schedulerv1pb.Scheduler_WatchJobsServer) error) *Fake {
	f.addWatchFn = fn
	return f
}

func (f *Fake) Run(ctx context.Context) error {
	return f.runFn(ctx)
}

func (f *Fake) Client(ctx context.Context) (api.Interface, error) {
	return f.clientFn(ctx)
}

func (f *Fake) AddWatch(req *schedulerv1pb.WatchJobsRequestInitial, srv schedulerv1pb.Scheduler_WatchJobsServer) error {
	return f.addWatchFn(req, srv)
}
