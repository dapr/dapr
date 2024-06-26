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

package fake

import (
	"context"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
)

type Fake struct {
	addOrUpdateOutboxFn         func(stateStore v1alpha1.Component)
	enabledFn                   func(stateStore string) bool
	publishInternalFn           func(ctx context.Context, stateStore string, states []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error)
	subscribeToInternalTopicsFn func(ctx context.Context, appID string) error
}

func New() *Fake {
	return &Fake{
		addOrUpdateOutboxFn: func(stateStore v1alpha1.Component) {},
		enabledFn:           func(stateStore string) bool { return false },
		publishInternalFn: func(ctx context.Context, stateStore string, states []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error) {
			return nil, nil
		},
		subscribeToInternalTopicsFn: func(ctx context.Context, appID string) error { return nil },
	}
}

func (f *Fake) WithAddOrUpdateOutbox(fn func(stateStore v1alpha1.Component)) *Fake {
	f.addOrUpdateOutboxFn = fn
	return f
}

func (f *Fake) WithEnabled(fn func(stateStore string) bool) *Fake {
	f.enabledFn = fn
	return f
}

func (f *Fake) WithPublishInternal(fn func(ctx context.Context, stateStore string, states []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error)) *Fake {
	f.publishInternalFn = fn
	return f
}

func (f *Fake) WithSubscribeToInternalTopics(fn func(ctx context.Context, appID string) error) *Fake {
	f.subscribeToInternalTopicsFn = fn
	return f
}

func (f *Fake) AddOrUpdateOutbox(stateStore v1alpha1.Component) {
	f.addOrUpdateOutboxFn(stateStore)
}

func (f *Fake) Enabled(stateStore string) bool {
	return f.enabledFn(stateStore)
}

func (f *Fake) PublishInternal(ctx context.Context, stateStore string, states []state.TransactionalStateOperation, source, traceID, traceState string) ([]state.TransactionalStateOperation, error) {
	return f.publishInternalFn(ctx, stateStore, states, source, traceID, traceState)
}

func (f *Fake) SubscribeToInternalTopics(ctx context.Context, appID string) error {
	return f.subscribeToInternalTopicsFn(ctx, appID)
}
