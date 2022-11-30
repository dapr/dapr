/*
Copyright 2022 The Dapr Authors
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

package server

import (
	"context"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ListInputBindings implements runtime.AppCallbackServer
func (*Server) ListInputBindings(context.Context, *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	panic("unimplemented")
}

// OnBindingEvent implements runtime.AppCallbackServer
func (*Server) OnBindingEvent(context.Context, *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	panic("unimplemented")
}

// OnInvoke implements runtime.AppCallbackServer
func (*Server) OnInvoke(context.Context, *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	panic("unimplemented")
}
