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

package messaging

import (
	"bytes"
	"context"
	"testing"

	grpc "google.golang.org/grpc"

	grpcMetadata "google.golang.org/grpc/metadata"

	emptypb "google.golang.org/protobuf/types/known/emptypb"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
)

type fakeStream struct {
	ff  *fuzz.ConsumeFuzzer
	ctx context.Context
}

func (f *fakeStream) Context() context.Context {
	if f.ctx == nil {
		return context.Background()
	}
	return f.ctx
}

func (f *fakeStream) Header() (grpcMetadata.MD, error) {
	md := make(grpcMetadata.MD)
	err := f.ff.FuzzMap(&md)
	return md, err
}

func (f *fakeStream) SetHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeStream) SendHeader(grpcMetadata.MD) error {
	return nil
}

func (f *fakeStream) SetTrailer(grpcMetadata.MD) {
}

func (f *fakeStream) Trailer() grpcMetadata.MD {
	md := make(grpcMetadata.MD)
	f.ff.FuzzMap(&md)
	return md
}

func (f *fakeStream) SendMsg(m any) error {
	return nil
}

func (f *fakeStream) RecvMsg(chunk interface{}) error {
	resp := &internalv1pb.InternalInvokeResponse{}
	payload := &v1.StreamPayload{}
	f.ff.GenerateStruct(resp)
	f.ff.GenerateStruct(payload)
	chunk.(*internalv1pb.InternalInvokeResponseStream).Response = resp
	chunk.(*internalv1pb.InternalInvokeResponseStream).Payload = payload
	return nil
}

func (f *fakeStream) CloseSend() error {
	return nil
}

func (f *fakeStream) Send(*internalv1pb.InternalInvokeRequestStream) error {
	return nil
}

func (f *fakeStream) Recv() (*internalv1pb.InternalInvokeResponseStream, error) {
	return &internalv1pb.InternalInvokeResponseStream{}, nil
}

type serviceInvocationClientForFuzing struct {
	ff *fuzz.ConsumeFuzzer
}

func (c *serviceInvocationClientForFuzing) CallActor(ctx context.Context, in *internalv1pb.InternalInvokeRequest, opts ...grpc.CallOption) (*internalv1pb.InternalInvokeResponse, error) {
	return &internalv1pb.InternalInvokeResponse{}, nil
}

func (c *serviceInvocationClientForFuzing) CallActorReminder(ctx context.Context, in *internalv1pb.Reminder, opts ...grpc.CallOption) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (c *serviceInvocationClientForFuzing) CallLocal(ctx context.Context, in *internalv1pb.InternalInvokeRequest, opts ...grpc.CallOption) (*internalv1pb.InternalInvokeResponse, error) {
	return &internalv1pb.InternalInvokeResponse{}, nil
}

func (c *serviceInvocationClientForFuzing) CallLocalStream(ctx context.Context, opts ...grpc.CallOption) (internalv1pb.ServiceInvocation_CallLocalStreamClient, error) {
	return &fakeStream{
		ff:  c.ff,
		ctx: context.Background(),
	}, nil
}

// Tests invokeRemoteStream with a randomized request.
// This test needs some mocked services which are
// implemented above.
func FuzzInvokeRemote(f *testing.F) {
	f.Fuzz(func(t *testing.T, data1, data2, data3 []byte, actorType, actorID string) {
		ff := fuzz.NewConsumer(data1)
		ff.AllowUnexportedFields()
		ir := &v1.InvokeRequest{}
		ff.GenerateStruct(ir)
		md := make(map[string][]string)
		ff.FuzzMap(&md)
		r := invokev1.FromInvokeRequestMessage(ir).
			WithRawData(bytes.NewReader(data2)).
			WithRawDataBytes(data3).
			WithActor(actorType, actorID).
			WithMetadata(md)
		d := &directMessaging{}
		c := &serviceInvocationClientForFuzing{
			ff: ff,
		}
		_, _ = d.invokeRemoteStream(context.Background(), c, r, "appID", nil)
	})
}
