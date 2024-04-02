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

	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// options contains the options for running a pubsub subscriber gRPC server app.
type options struct {
	listTopicSubFn func(ctx context.Context, in *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error)
}

func WithListTopicSubscriptions(fn func(ctx context.Context, in *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listTopicSubFn = fn
	}
}
