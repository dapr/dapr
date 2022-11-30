// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.20.1
// source: proto/v1/notifier.proto

package pubsub_bulk_subscribe_grpc

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.32.0 or later.
const _ = grpc.SupportPackageIsVersion7

// PerfTestNotifierClient is the client API for PerfTestNotifier service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type PerfTestNotifierClient interface {
	// Subscribe to receive notifications about new test results.
	Subscribe(ctx context.Context, in *Request, opts ...grpc.CallOption) (PerfTestNotifier_SubscribeClient, error)
}

type perfTestNotifierClient struct {
	cc grpc.ClientConnInterface
}

func NewPerfTestNotifierClient(cc grpc.ClientConnInterface) PerfTestNotifierClient {
	return &perfTestNotifierClient{cc}
}

func (c *perfTestNotifierClient) Subscribe(ctx context.Context, in *Request, opts ...grpc.CallOption) (PerfTestNotifier_SubscribeClient, error) {
	stream, err := c.cc.NewStream(ctx, &PerfTestNotifier_ServiceDesc.Streams[0], "/test.proto.v1.PerfTestNotifier/Subscribe", opts...)
	if err != nil {
		return nil, err
	}
	x := &perfTestNotifierSubscribeClient{stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

type PerfTestNotifier_SubscribeClient interface {
	Recv() (*Response, error)
	grpc.ClientStream
}

type perfTestNotifierSubscribeClient struct {
	grpc.ClientStream
}

func (x *perfTestNotifierSubscribeClient) Recv() (*Response, error) {
	m := new(Response)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

// PerfTestNotifierServer is the server API for PerfTestNotifier service.
// All implementations must embed UnimplementedPerfTestNotifierServer
// for forward compatibility
type PerfTestNotifierServer interface {
	// Subscribe to receive notifications about new test results.
	Subscribe(*Request, PerfTestNotifier_SubscribeServer) error
	mustEmbedUnimplementedPerfTestNotifierServer()
}

// UnimplementedPerfTestNotifierServer must be embedded to have forward compatible implementations.
type UnimplementedPerfTestNotifierServer struct {
}

func (UnimplementedPerfTestNotifierServer) Subscribe(*Request, PerfTestNotifier_SubscribeServer) error {
	return status.Errorf(codes.Unimplemented, "method Subscribe not implemented")
}
func (UnimplementedPerfTestNotifierServer) mustEmbedUnimplementedPerfTestNotifierServer() {}

// UnsafePerfTestNotifierServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to PerfTestNotifierServer will
// result in compilation errors.
type UnsafePerfTestNotifierServer interface {
	mustEmbedUnimplementedPerfTestNotifierServer()
}

func RegisterPerfTestNotifierServer(s grpc.ServiceRegistrar, srv PerfTestNotifierServer) {
	s.RegisterService(&PerfTestNotifier_ServiceDesc, srv)
}

func _PerfTestNotifier_Subscribe_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(Request)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(PerfTestNotifierServer).Subscribe(m, &perfTestNotifierSubscribeServer{stream})
}

type PerfTestNotifier_SubscribeServer interface {
	Send(*Response) error
	grpc.ServerStream
}

type perfTestNotifierSubscribeServer struct {
	grpc.ServerStream
}

func (x *perfTestNotifierSubscribeServer) Send(m *Response) error {
	return x.ServerStream.SendMsg(m)
}

// PerfTestNotifier_ServiceDesc is the grpc.ServiceDesc for PerfTestNotifier service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var PerfTestNotifier_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "test.proto.v1.PerfTestNotifier",
	HandlerType: (*PerfTestNotifierServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Subscribe",
			Handler:       _PerfTestNotifier_Subscribe_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "proto/v1/notifier.proto",
}
