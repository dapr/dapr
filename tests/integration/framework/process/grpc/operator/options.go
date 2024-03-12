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

package operator

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	operatorv1 "github.com/dapr/dapr/pkg/proto/operator/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
)

// options contains the options for running a GRPC server in integration tests.
type options struct {
	grpcopts []procgrpc.Option
	sentry   *sentry.Sentry

	withRegister          func(*grpc.Server)
	componentUpdateFn     func(*operatorv1.ComponentUpdateRequest, operatorv1.Operator_ComponentUpdateServer) error
	getConfigurationFn    func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error)
	getResiliencyFn       func(context.Context, *operatorv1.GetResiliencyRequest) (*operatorv1.GetResiliencyResponse, error)
	httpEndpointUpdateFn  func(*operatorv1.HTTPEndpointUpdateRequest, operatorv1.Operator_HTTPEndpointUpdateServer) error
	listComponentsFn      func(context.Context, *operatorv1.ListComponentsRequest) (*operatorv1.ListComponentResponse, error)
	listHTTPEndpointsFn   func(context.Context, *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error)
	listResiliencyFn      func(context.Context, *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error)
	listSubscriptionsFn   func(context.Context, *emptypb.Empty) (*operatorv1.ListSubscriptionsResponse, error)
	listSubscriptionsV2Fn func(context.Context, *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error)
}

func WithGRPCOptions(opts ...procgrpc.Option) func(*options) {
	return func(o *options) {
		o.grpcopts = opts
	}
}

func WithSentry(sentry *sentry.Sentry) func(*options) {
	return func(o *options) {
		o.sentry = sentry
	}
}

func WithComponentUpdateFn(fn func(*operatorv1.ComponentUpdateRequest, operatorv1.Operator_ComponentUpdateServer) error) func(*options) {
	return func(opts *options) {
		opts.componentUpdateFn = fn
	}
}

func WithGetConfigurationFn(fn func(context.Context, *operatorv1.GetConfigurationRequest) (*operatorv1.GetConfigurationResponse, error)) func(*options) {
	return func(opts *options) {
		opts.getConfigurationFn = fn
	}
}

func WithGetResiliencyFn(fn func(context.Context, *operatorv1.GetResiliencyRequest) (*operatorv1.GetResiliencyResponse, error)) func(*options) {
	return func(opts *options) {
		opts.getResiliencyFn = fn
	}
}

func WithHTTPEndpointUpdateFn(fn func(*operatorv1.HTTPEndpointUpdateRequest, operatorv1.Operator_HTTPEndpointUpdateServer) error) func(*options) {
	return func(opts *options) {
		opts.httpEndpointUpdateFn = fn
	}
}

func WithListComponentsFn(fn func(context.Context, *operatorv1.ListComponentsRequest) (*operatorv1.ListComponentResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listComponentsFn = fn
	}
}

func WithListHTTPEndpointsFn(fn func(context.Context, *operatorv1.ListHTTPEndpointsRequest) (*operatorv1.ListHTTPEndpointsResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listHTTPEndpointsFn = fn
	}
}

func WithListResiliencyFn(fn func(context.Context, *operatorv1.ListResiliencyRequest) (*operatorv1.ListResiliencyResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listResiliencyFn = fn
	}
}

func WithListSubscriptionsFn(fn func(context.Context, *emptypb.Empty) (*operatorv1.ListSubscriptionsResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listSubscriptionsFn = fn
	}
}

func WithListSubscriptionsV2Fn(fn func(context.Context, *operatorv1.ListSubscriptionsRequest) (*operatorv1.ListSubscriptionsResponse, error)) func(*options) {
	return func(opts *options) {
		opts.listSubscriptionsV2Fn = fn
	}
}
