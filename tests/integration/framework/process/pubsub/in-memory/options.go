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

package inmemory

import (
	"context"

	"github.com/dapr/components-contrib/pubsub"
)

// options contains the options for running an in-memory pubsub.
type options struct {
	publishFn func(ctx context.Context, req *pubsub.PublishRequest) error
	features  []pubsub.Feature
}

func WithFeatures(features ...pubsub.Feature) Option {
	return func(o *options) {
		o.features = features
	}
}

func WithPublishFn(fn func(ctx context.Context, req *pubsub.PublishRequest) error) Option {
	return func(o *options) {
		o.publishFn = fn
	}
}
