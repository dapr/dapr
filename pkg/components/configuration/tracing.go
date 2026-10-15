/*
Copyright 2026 The Dapr Authors
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

package configuration

import (
	"context"

	"github.com/dapr/components-contrib/configuration"
	diag "github.com/dapr/dapr/pkg/diagnostics"
)

// WithTracing wraps store so every call into it emits a client span, parented
// to the span in the call's context.
// Subscribe is traced as the call that registers the subscription; updates
// delivered to the handler afterwards are not traced.
func WithTracing(store configuration.Store, name, componentType string) configuration.Store {
	return &tracedStore{
		Store: store,
		info:  diag.ComponentSpanInfo{Name: name, Type: componentType},
	}
}

// tracedStore embeds the store so Init, Close, Features and
// GetComponentMetadata (present only under the metadata build tag) pass
// through unchanged.
type tracedStore struct {
	configuration.Store
	info diag.ComponentSpanInfo
}

func (t *tracedStore) Unwrap() configuration.Store {
	return t.Store
}

func (t *tracedStore) Get(ctx context.Context, req *configuration.GetRequest) (*configuration.GetResponse, error) {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.Get)
	res, err := t.Store.Get(ctx, req)
	diag.EndComponentSpan(span, err)
	return res, err
}

func (t *tracedStore) Subscribe(ctx context.Context, req *configuration.SubscribeRequest, handler configuration.UpdateHandler) (string, error) {
	// The component keeps ctx for the lifetime of the subscription, so do not
	// hand it a context carrying a span that ends when this call returns.
	_, span := diag.StartComponentSpan(ctx, t.info, diag.ConfigurationSubscribe)
	id, err := t.Store.Subscribe(ctx, req, handler)
	diag.EndComponentSpan(span, err)
	return id, err
}

func (t *tracedStore) Unsubscribe(ctx context.Context, req *configuration.UnsubscribeRequest) error {
	ctx, span := diag.StartComponentSpan(ctx, t.info, diag.ConfigurationUnsubscribe)
	err := t.Store.Unsubscribe(ctx, req)
	diag.EndComponentSpan(span, err)
	return err
}
