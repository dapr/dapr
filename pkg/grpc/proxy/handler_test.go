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
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
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
	t                     *testing.T
	expectPingStreamError *atomic.Bool
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
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			if s.expectPingStreamError.Load() {
				require.Error(s.t, err, "should have failed reading stream")
			} else {
				require.NoError(s.t, err, "can't fail reading stream")
			}
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

type proxyTestSuite struct {
	suite.Suite

	serverListener   net.Listener
	server           *grpc.Server
	proxyListener    net.Listener
	proxy            *grpc.Server
	serverClientConn *grpc.ClientConn
	service          *assertingService
	lock             sync.Mutex

	client     *grpc.ClientConn
	testClient pb.TestServiceClient
}

func (s *proxyTestSuite) ctx() (context.Context, context.CancelFunc) {
	// Make all RPC calls last at most 5 sec, meaning all async issues or deadlock will not kill tests.
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (s *proxyTestSuite) TestPingEmptyCarriesClientMetadata() {
	ctx, cancel := s.ctx()
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(clientMdKey, "true"))
	out, err := s.testClient.PingEmpty(ctx, &pb.Empty{})
	require.NoError(s.T(), err, "PingEmpty should succeed without errors")
	require.Equal(s.T(), pingDefaultValue, out.Value)
	require.Equal(s.T(), int32(42), out.Counter)
}

func (s *proxyTestSuite) TestPingEmpty_StressTest() {
	for i := 0; i < 50; i++ {
		s.TestPingEmptyCarriesClientMetadata()
	}
}

func (s *proxyTestSuite) TestPingCarriesServerHeadersAndTrailers() {
	// s.T().Skip()
	headerMd := make(metadata.MD)
	trailerMd := make(metadata.MD)
	ctx, cancel := s.ctx()
	defer cancel()
	// This is an awkward calling convention... but meh.
	out, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: "foo"}, grpc.Header(&headerMd), grpc.Trailer(&trailerMd))
	require.NoError(s.T(), err, "Ping should succeed without errors")
	require.Equal(s.T(), "foo", out.Value)
	require.Equal(s.T(), int32(42), out.Counter)
	assert.Contains(s.T(), headerMd, serverHeaderMdKey, "server response headers must contain server data")
	assert.Len(s.T(), trailerMd, 1, "server response trailers must contain server data")
}

func (s *proxyTestSuite) TestPingErrorPropagatesAppError() {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.testClient.PingError(ctx, &pb.PingRequest{Value: "foo"})
	require.Error(s.T(), err, "PingError should never succeed")
	st, ok := status.FromError(err)
	require.True(s.T(), ok, "must get status from error")
	assert.Equal(s.T(), codes.FailedPrecondition, st.Code())
	assert.Equal(s.T(), "Userspace error.", st.Message())
}

func (s *proxyTestSuite) TestDirectorErrorIsPropagated() {
	ctx, cancel := s.ctx()
	defer cancel()
	// See SetupSuite where the StreamDirector has a special case.
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(rejectingMdKey, "true"))
	_, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: "foo"})
	require.Error(s.T(), err, "Director should reject this RPC")
	st, ok := status.FromError(err)
	require.True(s.T(), ok, "must get status from error")
	assert.Equal(s.T(), codes.PermissionDenied, st.Code())
	assert.Equal(s.T(), "testing rejection", st.Message())
}

func (s *proxyTestSuite) TestPingStream_FullDuplexWorks() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(StreamMetadataKey, "true"))
	stream, err := s.testClient.PingStream(ctx)
	require.NoError(s.T(), err, "PingStream request should be successful")

	for i := 0; i < countListResponses; i++ {
		if s.sendPing(stream, i) {
			break
		}
	}
	require.NoError(s.T(), stream.CloseSend(), "no error on close send")
	_, err = stream.Recv()
	require.ErrorIs(s.T(), err, io.EOF, "stream should close with io.EOF, meaining OK")
	// Check that the trailer headers are here.
	trailerMd := stream.Trailer()
	assert.Len(s.T(), trailerMd, 1, "PingStream trailer headers user contain metadata")
}

func (s *proxyTestSuite) TestPingStream_StressTest() {
	for i := 0; i < 50; i++ {
		s.TestPingStream_FullDuplexWorks()
	}
}

func (s *proxyTestSuite) TestPingStream_MultipleThreads() {
	wg := sync.WaitGroup{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			for j := 0; j < 10; j++ {
				s.TestPingStream_StressTest()
			}
			wg.Done()
		}()
	}

	ch := make(chan struct{})
	go func() {
		wg.Wait()
		close(ch)
	}()
	select {
	case <-time.After(time.Second * 10):
		assert.Fail(s.T(), "Timed out waiting for proxy to return.")
	case <-ch:
		return
	}
}

func (s *proxyTestSuite) TestRecoveryFromNetworkFailure() {
	// Make sure everything works before we break things
	s.TestPingEmptyCarriesClientMetadata()

	s.T().Run("Fails when no server is running", func(t *testing.T) {
		// Stop the server again
		s.stopServer(s.T())

		ctx, cancel := s.ctx()
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(clientMdKey, "true"))
		_, err := s.testClient.PingEmpty(ctx, &pb.Empty{})
		require.Error(t, err, "must fail to ping when server is down")
	})

	s.T().Run("Reconnects to new server", func(t *testing.T) {
		// Restart the server
		s.restartServer(s.T())

		s.TestPingEmptyCarriesClientMetadata()
	})
}

func (s *proxyTestSuite) sendPing(stream pb.TestService_PingStreamClient, i int) (eof bool) {
	ping := &pb.PingRequest{Value: fmt.Sprintf("foo:%d", i)}
	err := stream.Send(ping)
	require.NoError(s.T(), err, "sending to PingStream must not fail")
	resp, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return true
	}
	if i == 0 {
		// Check that the header arrives before all entries.
		headerMd, hErr := stream.Header()
		require.NoError(s.T(), hErr, "PingStream headers should not error.")
		assert.Contains(s.T(), headerMd, serverHeaderMdKey, "PingStream response headers user contain metadata")
	}
	require.NotNil(s.T(), resp, "resp must not be nil")
	assert.EqualValues(s.T(), i, resp.Counter, "ping roundtrip must succeed with the correct id")
	return false
}

func (s *proxyTestSuite) TestStreamConnectionInterrupted() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(StreamMetadataKey, "true"))
	stream, err := s.testClient.PingStream(ctx)
	require.NoError(s.T(), err, "PingStream request should be successful")

	// Send one message then interrupt the connection
	eof := s.sendPing(stream, 0)
	require.False(s.T(), eof)

	s.service.expectPingStreamError.Store(true)
	defer func() {
		s.service.expectPingStreamError.Store(false)
	}()
	s.stopServer(s.T())

	// Send another message, which should fail without resiliency
	ping := &pb.PingRequest{Value: fmt.Sprintf("foo:%d", 1)}
	err = stream.Send(ping)
	require.Error(s.T(), err, "sending to PingStream must fail with a stopped server")

	// Restart the server
	s.restartServer(s.T())

	// Pings should still fail with EOF because the strea is closed
	err = stream.Send(ping)
	require.Error(s.T(), err, "sending to PingStream must fail on a closed stream")
	assert.ErrorIs(s.T(), err, io.EOF)
}

func (s *proxyTestSuite) initServer() {
	s.server = grpc.NewServer()
	pb.RegisterTestServiceServer(s.server, s.service)
}

func (s *proxyTestSuite) stopServer(t *testing.T) {
	t.Helper()
	s.server.Stop()
	time.Sleep(250 * time.Millisecond)
}

func (s *proxyTestSuite) restartServer(t *testing.T) {
	t.Helper()
	var err error

	srvPort := s.serverListener.Addr().(*net.TCPAddr).Port
	s.serverListener, err = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(srvPort))
	require.NoError(s.T(), err, "must not error while starting serverListener")

	s.T().Logf("re-starting grpc.Server at: %v", s.serverListener.Addr().String())
	s.initServer()
	go s.server.Serve(s.serverListener)

	time.Sleep(250 * time.Millisecond)
}

func (s *proxyTestSuite) getServerClientConn() (conn *grpc.ClientConn, teardown func(bool), err error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	teardown = func(destroy bool) {
		s.lock.Lock()
		defer s.lock.Unlock()

		if destroy {
			s.serverClientConn.Close()
			s.serverClientConn = nil
		}
	}

	if s.serverClientConn == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, err = grpc.DialContext(
			ctx,
			s.serverListener.Addr().String(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
			grpc.WithBlock(),
		)
		if err != nil {
			return nil, teardown, err
		}
		s.serverClientConn = conn
	}

	return s.serverClientConn, teardown, nil
}

func (s *proxyTestSuite) SetupSuite() {
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

	s.service = &assertingService{
		t:                     s.T(),
		expectPingStreamError: &atomic.Bool{},
	}

	s.initServer()

	// Setup of the proxy's Director.
	director := func(ctx context.Context, fullName string) (context.Context, *grpc.ClientConn, *ProxyTarget, func(bool), error) {
		teardown := func(bool) {}
		target := &ProxyTarget{}
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			if _, exists := md[rejectingMdKey]; exists {
				return ctx, nil, target, teardown, status.Errorf(codes.PermissionDenied, "testing rejection")
			}
		}
		// Explicitly copy the metadata, otherwise the tests will fail.
		outCtx := metadata.NewOutgoingContext(ctx, md.Copy())
		conn, teardown, sErr := s.getServerClientConn()
		if sErr != nil {
			return ctx, nil, target, teardown, status.Errorf(codes.PermissionDenied, "testing rejection")
		}
		return outCtx, conn, target, teardown, nil
	}
	th := TransparentHandler(
		director,
		resiliency.New(nil),
		func(string) (bool, error) { return true, nil },
		func(ctx context.Context, address, id, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error) {
			return s.getServerClientConn()
		},
	)
	s.proxy = grpc.NewServer(
		grpc.UnknownServiceHandler(th),
	)
	// Ping handler is handled as an explicit registration and not as a TransparentHandler.
	RegisterService(s.proxy, director, resiliency.New(nil),
		"mwitkow.testproto.TestService",
		"Ping")

	// Start the serving loops.
	s.T().Logf("starting grpc.Server at: %v", s.serverListener.Addr().String())
	go s.server.Serve(s.serverListener)
	s.T().Logf("starting grpc.Proxy at: %v", s.proxyListener.Addr().String())
	go s.proxy.Serve(s.proxyListener)

	time.Sleep(500 * time.Millisecond)

	clientConn, err := grpc.DialContext(
		context.Background(),
		strings.Replace(s.proxyListener.Addr().String(), "127.0.0.1", "localhost", 1),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	require.NoError(s.T(), err, "must not error on deferred client Dial")
	s.testClient = pb.NewTestServiceClient(clientConn)
}

func (s *proxyTestSuite) TearDownSuite() {
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

func TestProxySuite(t *testing.T) {
	suite.Run(t, &proxyTestSuite{})
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
