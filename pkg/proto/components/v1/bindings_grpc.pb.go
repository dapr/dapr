// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.2.0
// - protoc             v3.21.12
// source: dapr/proto/components/v1/bindings.proto

package components

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

// InputBindingClient is the client API for InputBinding service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type InputBindingClient interface {
	// Initializes the inputbinding component component with the given metadata.
	Init(ctx context.Context, in *InputBindingInitRequest, opts ...grpc.CallOption) (*InputBindingInitResponse, error)
	// Establishes a stream with the server, which sends messages down to the
	// client. The client streams acknowledgements back to the server. The server
	// will close the stream and return the status on any error. In case of closed
	// connection, the client should re-establish the stream.
	Read(ctx context.Context, opts ...grpc.CallOption) (InputBinding_ReadClient, error)
	// Ping the InputBinding. Used for liveness porpuses.
	Ping(ctx context.Context, in *PingRequest, opts ...grpc.CallOption) (*PingResponse, error)
}

type inputBindingClient struct {
	cc grpc.ClientConnInterface
}

func NewInputBindingClient(cc grpc.ClientConnInterface) InputBindingClient {
	return &inputBindingClient{cc}
}

func (c *inputBindingClient) Init(ctx context.Context, in *InputBindingInitRequest, opts ...grpc.CallOption) (*InputBindingInitResponse, error) {
	out := new(InputBindingInitResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.InputBinding/Init", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *inputBindingClient) Read(ctx context.Context, opts ...grpc.CallOption) (InputBinding_ReadClient, error) {
	stream, err := c.cc.NewStream(ctx, &InputBinding_ServiceDesc.Streams[0], "/dapr.proto.components.v1.InputBinding/Read", opts...)
	if err != nil {
		return nil, err
	}
	x := &inputBindingReadClient{stream}
	return x, nil
}

type InputBinding_ReadClient interface {
	Send(*ReadRequest) error
	Recv() (*ReadResponse, error)
	grpc.ClientStream
}

type inputBindingReadClient struct {
	grpc.ClientStream
}

func (x *inputBindingReadClient) Send(m *ReadRequest) error {
	return x.ClientStream.SendMsg(m)
}

func (x *inputBindingReadClient) Recv() (*ReadResponse, error) {
	m := new(ReadResponse)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func (c *inputBindingClient) Ping(ctx context.Context, in *PingRequest, opts ...grpc.CallOption) (*PingResponse, error) {
	out := new(PingResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.InputBinding/Ping", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// InputBindingServer is the server API for InputBinding service.
// All implementations should embed UnimplementedInputBindingServer
// for forward compatibility
type InputBindingServer interface {
	// Initializes the inputbinding component component with the given metadata.
	Init(context.Context, *InputBindingInitRequest) (*InputBindingInitResponse, error)
	// Establishes a stream with the server, which sends messages down to the
	// client. The client streams acknowledgements back to the server. The server
	// will close the stream and return the status on any error. In case of closed
	// connection, the client should re-establish the stream.
	Read(InputBinding_ReadServer) error
	// Ping the InputBinding. Used for liveness porpuses.
	Ping(context.Context, *PingRequest) (*PingResponse, error)
}

// UnimplementedInputBindingServer should be embedded to have forward compatible implementations.
type UnimplementedInputBindingServer struct {
}

func (UnimplementedInputBindingServer) Init(context.Context, *InputBindingInitRequest) (*InputBindingInitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Init not implemented")
}
func (UnimplementedInputBindingServer) Read(InputBinding_ReadServer) error {
	return status.Errorf(codes.Unimplemented, "method Read not implemented")
}
func (UnimplementedInputBindingServer) Ping(context.Context, *PingRequest) (*PingResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Ping not implemented")
}

// UnsafeInputBindingServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to InputBindingServer will
// result in compilation errors.
type UnsafeInputBindingServer interface {
	mustEmbedUnimplementedInputBindingServer()
}

func RegisterInputBindingServer(s grpc.ServiceRegistrar, srv InputBindingServer) {
	s.RegisterService(&InputBinding_ServiceDesc, srv)
}

func _InputBinding_Init_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InputBindingInitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(InputBindingServer).Init(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.InputBinding/Init",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(InputBindingServer).Init(ctx, req.(*InputBindingInitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _InputBinding_Read_Handler(srv interface{}, stream grpc.ServerStream) error {
	return srv.(InputBindingServer).Read(&inputBindingReadServer{stream})
}

type InputBinding_ReadServer interface {
	Send(*ReadResponse) error
	Recv() (*ReadRequest, error)
	grpc.ServerStream
}

type inputBindingReadServer struct {
	grpc.ServerStream
}

func (x *inputBindingReadServer) Send(m *ReadResponse) error {
	return x.ServerStream.SendMsg(m)
}

func (x *inputBindingReadServer) Recv() (*ReadRequest, error) {
	m := new(ReadRequest)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

func _InputBinding_Ping_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PingRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(InputBindingServer).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.InputBinding/Ping",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(InputBindingServer).Ping(ctx, req.(*PingRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// InputBinding_ServiceDesc is the grpc.ServiceDesc for InputBinding service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var InputBinding_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "dapr.proto.components.v1.InputBinding",
	HandlerType: (*InputBindingServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Init",
			Handler:    _InputBinding_Init_Handler,
		},
		{
			MethodName: "Ping",
			Handler:    _InputBinding_Ping_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Read",
			Handler:       _InputBinding_Read_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "dapr/proto/components/v1/bindings.proto",
}

// OutputBindingClient is the client API for OutputBinding service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
type OutputBindingClient interface {
	// Initializes the outputbinding component component with the given metadata.
	Init(ctx context.Context, in *OutputBindingInitRequest, opts ...grpc.CallOption) (*OutputBindingInitResponse, error)
	// Invoke remote systems with optional payloads.
	Invoke(ctx context.Context, in *InvokeRequest, opts ...grpc.CallOption) (*InvokeResponse, error)
	// ListOperations list system supported operations.
	ListOperations(ctx context.Context, in *ListOperationsRequest, opts ...grpc.CallOption) (*ListOperationsResponse, error)
	// Ping the OutputBinding. Used for liveness porpuses.
	Ping(ctx context.Context, in *PingRequest, opts ...grpc.CallOption) (*PingResponse, error)
}

type outputBindingClient struct {
	cc grpc.ClientConnInterface
}

func NewOutputBindingClient(cc grpc.ClientConnInterface) OutputBindingClient {
	return &outputBindingClient{cc}
}

func (c *outputBindingClient) Init(ctx context.Context, in *OutputBindingInitRequest, opts ...grpc.CallOption) (*OutputBindingInitResponse, error) {
	out := new(OutputBindingInitResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.OutputBinding/Init", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *outputBindingClient) Invoke(ctx context.Context, in *InvokeRequest, opts ...grpc.CallOption) (*InvokeResponse, error) {
	out := new(InvokeResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.OutputBinding/Invoke", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *outputBindingClient) ListOperations(ctx context.Context, in *ListOperationsRequest, opts ...grpc.CallOption) (*ListOperationsResponse, error) {
	out := new(ListOperationsResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.OutputBinding/ListOperations", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *outputBindingClient) Ping(ctx context.Context, in *PingRequest, opts ...grpc.CallOption) (*PingResponse, error) {
	out := new(PingResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.components.v1.OutputBinding/Ping", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// OutputBindingServer is the server API for OutputBinding service.
// All implementations should embed UnimplementedOutputBindingServer
// for forward compatibility
type OutputBindingServer interface {
	// Initializes the outputbinding component component with the given metadata.
	Init(context.Context, *OutputBindingInitRequest) (*OutputBindingInitResponse, error)
	// Invoke remote systems with optional payloads.
	Invoke(context.Context, *InvokeRequest) (*InvokeResponse, error)
	// ListOperations list system supported operations.
	ListOperations(context.Context, *ListOperationsRequest) (*ListOperationsResponse, error)
	// Ping the OutputBinding. Used for liveness porpuses.
	Ping(context.Context, *PingRequest) (*PingResponse, error)
}

// UnimplementedOutputBindingServer should be embedded to have forward compatible implementations.
type UnimplementedOutputBindingServer struct {
}

func (UnimplementedOutputBindingServer) Init(context.Context, *OutputBindingInitRequest) (*OutputBindingInitResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Init not implemented")
}
func (UnimplementedOutputBindingServer) Invoke(context.Context, *InvokeRequest) (*InvokeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Invoke not implemented")
}
func (UnimplementedOutputBindingServer) ListOperations(context.Context, *ListOperationsRequest) (*ListOperationsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListOperations not implemented")
}
func (UnimplementedOutputBindingServer) Ping(context.Context, *PingRequest) (*PingResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Ping not implemented")
}

// UnsafeOutputBindingServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to OutputBindingServer will
// result in compilation errors.
type UnsafeOutputBindingServer interface {
	mustEmbedUnimplementedOutputBindingServer()
}

func RegisterOutputBindingServer(s grpc.ServiceRegistrar, srv OutputBindingServer) {
	s.RegisterService(&OutputBinding_ServiceDesc, srv)
}

func _OutputBinding_Init_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(OutputBindingInitRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OutputBindingServer).Init(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.OutputBinding/Init",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OutputBindingServer).Init(ctx, req.(*OutputBindingInitRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OutputBinding_Invoke_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InvokeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OutputBindingServer).Invoke(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.OutputBinding/Invoke",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OutputBindingServer).Invoke(ctx, req.(*InvokeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OutputBinding_ListOperations_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ListOperationsRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OutputBindingServer).ListOperations(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.OutputBinding/ListOperations",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OutputBindingServer).ListOperations(ctx, req.(*ListOperationsRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _OutputBinding_Ping_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(PingRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(OutputBindingServer).Ping(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.components.v1.OutputBinding/Ping",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(OutputBindingServer).Ping(ctx, req.(*PingRequest))
	}
	return interceptor(ctx, in, info, handler)
}

// OutputBinding_ServiceDesc is the grpc.ServiceDesc for OutputBinding service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var OutputBinding_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "dapr.proto.components.v1.OutputBinding",
	HandlerType: (*OutputBindingServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Init",
			Handler:    _OutputBinding_Init_Handler,
		},
		{
			MethodName: "Invoke",
			Handler:    _OutputBinding_Invoke_Handler,
		},
		{
			MethodName: "ListOperations",
			Handler:    _OutputBinding_ListOperations_Handler,
		},
		{
			MethodName: "Ping",
			Handler:    _OutputBinding_Ping_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "dapr/proto/components/v1/bindings.proto",
}
