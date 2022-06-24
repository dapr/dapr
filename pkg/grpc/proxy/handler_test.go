/*
Copyright 2021 The Dapr Authors
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

// Based on https://github.com/trusch/grpc-proxy
// Copyright Michal Witkowski. Licensed under Apache2 license: https://github.com/trusch/grpc-proxy/blob/master/LICENSE.txt

package proxy

import (
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	codec "github.com/dapr/dapr/pkg/grpc/proxy/codec"
	pb "github.com/dapr/dapr/pkg/grpc/proxy/testservice"
	"github.com/dapr/dapr/pkg/resiliency"
)

const (
	pingDefaultValue   = "I like kittens."
	clientMdKey        = "test-client-header"
	serverHeaderMdKey  = "test-client-header"
	serverTrailerMdKey = "test-client-trailer"

	rejectingMdKey = "test-reject-rpc-if-in-context"

	countListResponses = 20
)

// asserting service is implemented on the server side and serves as a handler for stuff.
type assertingService struct {
	pb.UnimplementedTestServiceServer
	t *testing.T
}

func (s *assertingService) PingEmpty(ctx context.Context, _ *pb.Empty) (*pb.PingResponse, error) {
	// Check that this call has client's metadata.
	md, ok := metadata.FromIncomingContext(ctx)
	assert.True(s.t, ok, "PingEmpty call must have metadata in context")
	_, ok = md[clientMdKey]
	assert.True(s.t, ok, "PingEmpty call must have clients's custom headers in metadata")
	return &pb.PingResponse{Value: pingDefaultValue, Counter: 42}, nil
}

func (s *assertingService) Ping(ctx context.Context, ping *pb.PingRequest) (*pb.PingResponse, error) {
	// Send user trailers and headers.
	grpc.SendHeader(ctx, metadata.Pairs(serverHeaderMdKey, "I like turtles."))
	grpc.SetTrailer(ctx, metadata.Pairs(serverTrailerMdKey, "I like ending turtles."))
	return &pb.PingResponse{Value: ping.Value, Counter: 42}, nil
}

func (s *assertingService) PingError(ctx context.Context, ping *pb.PingRequest) (*pb.Empty, error) {
	return nil, status.Errorf(codes.FailedPrecondition, "Userspace error.")
}

func (s *assertingService) PingList(ping *pb.PingRequest, stream pb.TestService_PingListServer) error {
	// Send user trailers and headers.
	stream.SendHeader(metadata.Pairs(serverHeaderMdKey, "I like turtles."))
	for i := 0; i < countListResponses; i++ {
		stream.Send(&pb.PingResponse{Value: ping.Value, Counter: int32(i)})
	}
	stream.SetTrailer(metadata.Pairs(serverTrailerMdKey, "I like ending turtles."))
	return nil
}

func (s *assertingService) PingStream(stream pb.TestService_PingStreamServer) error {
	stream.SendHeader(metadata.Pairs(serverHeaderMdKey, "I like turtles."))
	counter := int32(0)
	for {
		ping, err := stream.Recv()
		if err == io.EOF {
			break
		} else if err != nil {
			require.NoError(s.t, err, "can't fail reading stream")
			return err
		}
		pong := &pb.PingResponse{Value: ping.Value, Counter: counter}
		if err := stream.Send(pong); err != nil {
			require.NoError(s.t, err, "can't fail sending back a pong")
		}
		counter++
	}
	stream.SetTrailer(metadata.Pairs(serverTrailerMdKey, "I like ending turtles."))
	return nil
}

// ProxyHappySuite tests the "happy" path of handling: that everything works in absence of connection issues.
type ProxyHappySuite struct {
	suite.Suite

	serverListener   net.Listener
	server           *grpc.Server
	proxyListener    net.Listener
	proxy            *grpc.Server
	serverClientConn *grpc.ClientConn

	client     *grpc.ClientConn
	testClient pb.TestServiceClient
}

func (s *ProxyHappySuite) ctx() context.Context {
	// Make all RPC calls last at most 1 sec, meaning all async issues or deadlock will not kill tests.
	ctx, _ := context.WithTimeout(context.TODO(), 120*time.Second)
	return ctx
}

func (s *ProxyHappySuite) TestPingEmptyCarriesClientMetadata() {
	// s.T().Skip()
	ctx := metadata.NewOutgoingContext(s.ctx(), metadata.Pairs(clientMdKey, "true"))
	out, err := s.testClient.PingEmpty(ctx, &pb.Empty{})
	require.NoError(s.T(), err, "PingEmpty should succeed without errors")
	require.Equal(s.T(), pingDefaultValue, out.Value)
	require.Equal(s.T(), int32(42), out.Counter)
}

func (s *ProxyHappySuite) TestPingEmpty_StressTest() {
	for i := 0; i < 50; i++ {
		s.TestPingEmptyCarriesClientMetadata()
	}
}

func (s *ProxyHappySuite) TestPingCarriesServerHeadersAndTrailers() {
	// s.T().Skip()
	headerMd := make(metadata.MD)
	trailerMd := make(metadata.MD)
	// This is an awkward calling convention... but meh.
	out, err := s.testClient.Ping(s.ctx(), &pb.PingRequest{Value: "foo"}, grpc.Header(&headerMd), grpc.Trailer(&trailerMd))
	require.NoError(s.T(), err, "Ping should succeed without errors")
	require.Equal(s.T(), "foo", out.Value)
	require.Equal(s.T(), int32(42), out.Counter)
	assert.Contains(s.T(), headerMd, serverHeaderMdKey, "server response headers must contain server data")
	assert.Len(s.T(), trailerMd, 1, "server response trailers must contain server data")
}

func (s *ProxyHappySuite) TestPingErrorPropagatesAppError() {
	_, err := s.testClient.PingError(s.ctx(), &pb.PingRequest{Value: "foo"})
	require.Error(s.T(), err, "PingError should never succeed")
	st, ok := status.FromError(err)
	require.True(s.T(), ok, "must get status from error")
	assert.Equal(s.T(), codes.FailedPrecondition, st.Code())
	assert.Equal(s.T(), "Userspace error.", st.Message())
}

func (s *ProxyHappySuite) TestDirectorErrorIsPropagated() {
	// See SetupSuite where the StreamDirector has a special case.
	ctx := metadata.NewOutgoingContext(s.ctx(), metadata.Pairs(rejectingMdKey, "true"))
	_, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: "foo"})
	require.Error(s.T(), err, "Director should reject this RPC")
	st, ok := status.FromError(err)
	require.True(s.T(), ok, "must get status from error")
	assert.Equal(s.T(), codes.PermissionDenied, st.Code())
	assert.Equal(s.T(), "testing rejection", st.Message())
}

func (s *ProxyHappySuite) TestPingStream_FullDuplexWorks() {
	stream, err := s.testClient.PingStream(s.ctx())
	require.NoError(s.T(), err, "PingStream request should be successful.")

	for i := 0; i < countListResponses; i++ {
		ping := &pb.PingRequest{Value: fmt.Sprintf("foo:%d", i)}
		require.NoError(s.T(), stream.Send(ping), "sending to PingStream must not fail")
		resp, sErr := stream.Recv()
		if sErr == io.EOF {
			break
		}
		if i == 0 {
			// Check that the header arrives before all entries.
			headerMd, hErr := stream.Header()
			require.NoError(s.T(), hErr, "PingStream headers should not error.")
			assert.Contains(s.T(), headerMd, serverHeaderMdKey, "PingStream response headers user contain metadata")
		}
		require.NotNil(s.T(), resp, "resp must not be nil")
		assert.EqualValues(s.T(), i, resp.Counter, "ping roundtrip must succeed with the correct id")
	}
	require.NoError(s.T(), stream.CloseSend(), "no error on close send")
	_, err = stream.Recv()
	require.Equal(s.T(), io.EOF, err, "stream should close with io.EOF, meaining OK")
	// Check that the trailer headers are here.
	trailerMd := stream.Trailer()
	assert.Len(s.T(), trailerMd, 1, "PingList trailer headers user contain metadata")
}

func (s *ProxyHappySuite) TestPingStream_StressTest() {
	for i := 0; i < 50; i++ {
		s.TestPingStream_FullDuplexWorks()
	}
}

func (s *ProxyHappySuite) SetupSuite() {
	var err error

	pc := encoding.GetCodec((&codec.Proxy{}).Name())
	dc := encoding.GetCodec("proto")
	require.NotNil(s.T(), pc, "proxy codec must be registered")
	require.NotNil(s.T(), dc, "default codec must be registered")

	s.proxyListener, err = net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err, "must be able to allocate a port for proxyListener")
	s.serverListener, err = net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err, "must be able to allocate a port for serverListener")

	grpclog.SetLoggerV2(testingLog{s.T()})

	s.server = grpc.NewServer()
	pb.RegisterTestServiceServer(s.server, &assertingService{t: s.T()})

	// Setup of the proxy's Director.
	s.serverClientConn, err = grpc.Dial(
		s.serverListener.Addr().String(),
		grpc.WithInsecure(),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	require.NoError(s.T(), err, "must not error on deferred client Dial")
	director := func(ctx context.Context, fullName string) (context.Context, *grpc.ClientConn, func(), error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if _, exists := md[rejectingMdKey]; exists {
				return ctx, nil, func() {}, status.Errorf(codes.PermissionDenied, "testing rejection")
			}
		}
		// Explicitly copy the metadata, otherwise the tests will fail.
		outCtx, _ := context.WithCancel(ctx)
		outCtx = metadata.NewOutgoingContext(outCtx, md.Copy())
		return outCtx, s.serverClientConn, func() {}, nil
	}
	s.proxy = grpc.NewServer(
		grpc.UnknownServiceHandler(TransparentHandler(director, resiliency.New(nil), func(string) (bool, error) { return true, nil })),
	)
	// Ping handler is handled as an explicit registration and not as a TransparentHandler.
	RegisterService(s.proxy, director, resiliency.New(nil),
		"mwitkow.testproto.TestService",
		"Ping")

	// Start the serving loops.
	s.T().Logf("starting grpc.Server at: %v", s.serverListener.Addr().String())
	go func() {
		s.server.Serve(s.serverListener)
	}()
	s.T().Logf("starting grpc.Proxy at: %v", s.proxyListener.Addr().String())
	go func() {
		s.proxy.Serve(s.proxyListener)
	}()

	time.Sleep(time.Second)

	ctx, cancel := context.WithTimeout(context.TODO(), time.Second*1)
	defer cancel()

	clientConn, err := grpc.DialContext(
		ctx,
		strings.Replace(s.proxyListener.Addr().String(), "127.0.0.1", "localhost", 1),
		grpc.WithInsecure(),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	require.NoError(s.T(), err, "must not error on deferred client Dial")
	s.testClient = pb.NewTestServiceClient(clientConn)
}

func (s *ProxyHappySuite) TearDownSuite() {
	if s.client != nil {
		s.client.Close()
	}
	if s.serverClientConn != nil {
		s.serverClientConn.Close()
	}
	// Close all transports so the logs don't get spammy.
	time.Sleep(10 * time.Millisecond)
	if s.proxy != nil {
		s.proxy.Stop()
		s.proxyListener.Close()
	}
	if s.serverListener != nil {
		s.server.Stop()
		s.serverListener.Close()
	}
}

func TestProxyHappySuite(t *testing.T) {
	suite.Run(t, &ProxyHappySuite{})
}

// Abstraction that allows us to pass the *testing.T as a grpclogger.
type testingLog struct {
	T *testing.T
}

// Info logs to INFO log. Arguments are handled in the manner of fmt.Print.
func (t testingLog) Info(args ...interface{}) {
}

// Infoln logs to INFO log. Arguments are handled in the manner of fmt.Println.
func (t testingLog) Infoln(args ...interface{}) {
}

// Infof logs to INFO log. Arguments are handled in the manner of fmt.Printf.
func (t testingLog) Infof(format string, args ...interface{}) {
}

// Warning logs to WARNING log. Arguments are handled in the manner of fmt.Print.
func (t testingLog) Warning(args ...interface{}) {
}

// Warningln logs to WARNING log. Arguments are handled in the manner of fmt.Println.
func (t testingLog) Warningln(args ...interface{}) {
}

// Warningf logs to WARNING log. Arguments are handled in the manner of fmt.Printf.
func (t testingLog) Warningf(format string, args ...interface{}) {
}

// Error logs to ERROR log. Arguments are handled in the manner of fmt.Print.
func (t testingLog) Error(args ...interface{}) {
	t.T.Error(args...)
}

// Errorln logs to ERROR log. Arguments are handled in the manner of fmt.Println.
func (t testingLog) Errorln(args ...interface{}) {
	t.T.Error(args...)
}

// Errorf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
func (t testingLog) Errorf(format string, args ...interface{}) {
	t.T.Errorf(format, args...)
}

// Fatal logs to ERROR log. Arguments are handled in the manner of fmt.Print.
// gRPC ensures that all Fatal logs will exit with os.Exit(1).
// Implementations may also call os.Exit() with a non-zero exit code.
func (t testingLog) Fatal(args ...interface{}) {
	t.T.Fatal(args...)
}

// Fatalln logs to ERROR log. Arguments are handled in the manner of fmt.Println.
// gRPC ensures that all Fatal logs will exit with os.Exit(1).
// Implementations may also call os.Exit() with a non-zero exit code.
func (t testingLog) Fatalln(args ...interface{}) {
	t.T.Fatal(args...)
}

// Fatalf logs to ERROR log. Arguments are handled in the manner of fmt.Printf.
// gRPC ensures that all Fatal logs will exit with os.Exit(1).
// Implementations may also call os.Exit() with a non-zero exit code.
func (t testingLog) Fatalf(format string, args ...interface{}) {
	t.T.Fatalf(format, args...)
}

// V reports whether verbosity level l is at least the requested verbose level.
func (t testingLog) V(l int) bool {
	return true
}
