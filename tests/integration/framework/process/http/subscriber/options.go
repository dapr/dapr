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

package subscriber

import (
	"context"
	"net/http"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
)

type options struct {
	routes       []string
	bulkRoutes   []string
	handlerFuncs []app.Option
	progSubs     *[]SubscriptionJSON
	initHealth   bool
	bulkStatusFn func(context.Context, []string) map[string]contribpubsub.AppResponseStatus
}

func WithRoutes(routes ...string) Option {
	return func(o *options) {
		o.routes = append(o.routes, routes...)
	}
}

func WithBulkRoutes(routes ...string) Option {
	return func(o *options) {
		o.bulkRoutes = append(o.bulkRoutes, routes...)
	}
}

// WithBulkStatusFn sets the status a bulk route answers each entry of a
// delivery with, given the delivery's entry IDs in order. Entries without a
// status are answered SUCCESS. fn is called with the delivery's request
// context, which it may block on to hold the delivery until daprd cancels it.
func WithBulkStatusFn(fn func(ctx context.Context, entryIDs []string) map[string]contribpubsub.AppResponseStatus) Option {
	return func(o *options) {
		o.bulkStatusFn = fn
	}
}

func WithHandlerFunc(path string, fn http.HandlerFunc) Option {
	return func(o *options) {
		o.handlerFuncs = append(o.handlerFuncs, app.WithHandlerFunc(path, fn))
	}
}

func WithProgrammaticSubscriptions(subs ...SubscriptionJSON) Option {
	return func(o *options) {
		if o.progSubs == nil {
			o.progSubs = new([]SubscriptionJSON)
		}
		*o.progSubs = append(*o.progSubs, subs...)
	}
}

func WithInitialHealth(health bool) Option {
	return func(o *options) {
		o.initHealth = health
	}
}
