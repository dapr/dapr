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

	codec "github.com/dapr/dapr/pkg/api/grpc/proxy/codec"
	pb "github.com/dapr/dapr/pkg/api/grpc/proxy/testservice"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/retry"

	"go.opencensus.io/stats/view"
	"go.opencensus.io/tag"
)

const (
	pingDefaultValue   = "I like kittens."
	clientMdKey        = "test-client-header"
	serverHeaderMdKey  = "test-client-header"
	serverTrailerMdKey = "test-client-trailer"

	rejectingMdKey = "test-reject-rpc-if-in-context"

	countListResponses = 20

	testAppID = "test"
)

const (
	serviceInvocationRequestSentName  = "runtime/service_invocation/req_sent_total"
	serviceInvocationResponseRecvName = "runtime/service_invocation/res_recv_total"
)

func metricsCleanup() {
	diag.CleanupRegisteredViews(
		serviceInvocationRequestSentName,
		serviceInvocationResponseRecvName)
}

var testLogger = logger.NewLogger("proxy-test")

// asserting service is implemented on the server side and serves as a handler for stuff.
type assertingService struct {
	pb.UnimplementedTestServiceServer
	t                          *testing.T
	expectPingStreamError      *atomic.Bool
	simulatePingFailures       *atomic.Int32
	pingCallCount              *atomic.Int32
	simulateDelay              *atomic.Int32
	simulateRandomFailures     *atomic.Bool
	simulateConnectionFailures *atomic.Int32
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
	count := s.pingCallCount.Add(1)

	// Simulate failures
	if delay := s.simulateDelay.Load(); delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	if s.simulatePingFailures.Add(-1) >= 0 {
		return nil, status.Errorf(codes.Internal, "Simulated failure")
	} else {
		s.simulatePingFailures.Store(0)
	}
	if s.simulateRandomFailures.Load() {
		if count%14 == 0 {
			time.Sleep(800 * time.Millisecond)
		} else if count%12 == 0 {
			return nil, status.Errorf(codes.Internal, "Simulated random failure")
		}
	}

	// Send user trailers and headers.
	grpc.SendHeader(ctx, metadata.Pairs(serverHeaderMdKey, "I like cats."))
	grpc.SetTrailer(ctx, metadata.Pairs(serverTrailerMdKey, "I also like dogs."))
	return &pb.PingResponse{Value: ping.GetValue(), Counter: 42}, nil
}

func (s *assertingService) PingError(ctx context.Context, ping *pb.PingRequest) (*pb.Empty, error) {
	return nil, status.Errorf(codes.FailedPrecondition, "Userspace error.")
}

func (s *assertingService) PingList(ping *pb.PingRequest, stream pb.TestService_PingListServer) error {
	// Send user trailers and headers.
	stream.SendHeader(metadata.Pairs(serverHeaderMdKey, "I like cats."))
	for i := 0; i < countListResponses; i++ {
		stream.Send(&pb.PingResponse{Value: ping.GetValue(), Counter: int32(i)})
	}
	stream.SetTrailer(metadata.Pairs(serverTrailerMdKey, "I also like dogs."))
	return nil
}

func (s *assertingService) PingStream(stream pb.TestService_PingStreamServer) error {
	var testName string
	{
		md, _ := metadata.FromIncomingContext(stream.Context())
		v := md["dapr-test"]
		if len(v) > 0 {
			testName = v[0]
		}
	}

	stream.SendHeader(metadata.Pairs(serverHeaderMdKey, "I like cats."))
	counter := int32(0)
	for {
		if delay := s.simulateDelay.Load(); delay > 0 {
			time.Sleep(time.Duration(delay) * time.Millisecond)
		}

		ping, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			if s.expectPingStreamError.Load() {
				require.Error(s.t, err, "should have failed reading stream - test name: "+testName)
			} else {
				// Do not fail in the case where error is context.Canceled which signifies the end of a test
				grpcStatus, ok := status.FromError(err)
				if !errors.Is(err, context.Canceled) && (!ok || grpcStatus.Code() != codes.Canceled || grpcStatus.Message() != "context canceled") {
					require.NoError(s.t, err, "can't fail reading stream - test name: "+testName)
				}
			}
			return err
		}
		pong := &pb.PingResponse{Value: ping.GetValue(), Counter: counter}
		if err := stream.Send(pong); err != nil {
			if s.expectPingStreamError.Load() {
				require.Error(s.t, err, "should have failed sending back a pong - test name: "+testName)
			} else {
				require.NoError(s.t, err, "can't fail sending back a pong - test name: "+testName)
			}
		}
		counter++
	}
	stream.SetTrailer(metadata.Pairs(serverTrailerMdKey, "I also like dogs."))
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
	policyDef        *resiliency.PolicyDefinition

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
	s.Require().NoError(err, "PingEmpty should succeed without errors")
	s.Require().Equal(pingDefaultValue, out.GetValue())
	s.Require().Equal(int32(42), out.GetCounter())
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
	s.Require().NoError(err, "Ping should succeed without errors")
	s.Require().Equal("foo", out.GetValue())
	s.Require().Equal(int32(42), out.GetCounter())
	s.Contains(headerMd, serverHeaderMdKey, "server response headers must contain server data")
	s.Len(trailerMd, 1, "server response headers must contain server data")
}

func (s *proxyTestSuite) TestPingErrorPropagatesAppError() {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.testClient.PingError(ctx, &pb.PingRequest{Value: "foo"})
	s.Require().Error(err, "PingError should never succeed")
	st, ok := status.FromError(err)
	s.Require().True(ok, "must get status from error")
	s.Equal(codes.FailedPrecondition, st.Code())
	s.Equal("Userspace error.", st.Message())
}

func (s *proxyTestSuite) TestDirectorErrorIsPropagated() {
	ctx, cancel := s.ctx()
	defer cancel()
	// See SetupSuite where the StreamDirector has a special case.
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(rejectingMdKey, "true"))
	_, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: "foo"})
	s.Require().Error(err, "Director should reject this RPC")
	st, ok := status.FromError(err)
	s.Require().True(ok, "must get status from error")
	s.Equal(codes.PermissionDenied, st.Code())
	s.Equal("testing rejection", st.Message())
}

func (s *proxyTestSuite) TestPingStream_FullDuplexWorks() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		// We are purposely not setting "dapr-stream" here so we can confirm that proxying of streamed requests works without the "dapr-stream" metadata as long as there's no resiliency policy with retries
		// Another test below will validate that, with retries enabled, an error is returned if "dapr-stream" is unset
		// StreamMetadataKey, "true",
		"dapr-test", s.T().Name(),
	))
	stream, err := s.testClient.PingStream(ctx)
	s.Require().NoError(err, "PingStream request should be successful")

	for i := 0; i < countListResponses; i++ {
		if s.sendPing(stream, i) {
			break
		}
	}
	s.Require().NoError(stream.CloseSend(), "no error on close send")
	_, err = stream.Recv()
	s.Require().ErrorIs(err, io.EOF, "stream should close with io.EOF, meaining OK")
	// Check that the trailer headers are here.
	trailerMd := stream.Trailer()
	s.Len(trailerMd, 1, "PingStream trailer headers user contain metadata")
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
		s.Fail("Timed out waiting for proxy to return.")
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
	s.Require().NoError(err, "sending to PingStream must not fail")
	resp, err := stream.Recv()
	if errors.Is(err, io.EOF) {
		return true
	}
	if i == 0 {
		// Check that the header arrives before all entries.
		headerMd, hErr := stream.Header()
		s.Require().NoError(hErr, "PingStream headers should not error.")
		s.Contains(headerMd, serverHeaderMdKey, "PingStream response headers user contain metadata")
	}
	s.Require().NotNil(resp, "resp must not be nil")
	s.EqualValues(i, resp.GetCounter(), "ping roundtrip must succeed with the correct id")
	return false
}

func (s *proxyTestSuite) TestStreamConnectionInterrupted() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
		StreamMetadataKey, "true",
		"dapr-test", s.T().Name(),
	))
	stream, err := s.testClient.PingStream(ctx)
	s.Require().NoError(err, "PingStream request should be successful")

	// Send one message then interrupt the connection
	eof := s.sendPing(stream, 0)
	s.Require().False(eof)

	s.service.expectPingStreamError.Store(true)
	defer func() {
		s.service.expectPingStreamError.Store(false)
	}()
	s.stopServer(s.T())

	// Send another message, which should fail without resiliency
	ping := &pb.PingRequest{Value: fmt.Sprintf("foo:%d", 1)}
	err = stream.Send(ping)
	s.Require().Error(err, "sending to PingStream must fail with a stopped server")

	// Restart the server
	s.restartServer(s.T())

	// Pings should still fail with EOF because the stream is closed
	err = stream.Send(ping)
	s.Require().Error(err, "sending to PingStream must fail on a closed stream")
	s.Require().ErrorIs(err, io.EOF)
}

func (s *proxyTestSuite) TestPingSimulateFailure() {
	ctx, cancel := s.ctx()
	defer cancel()

	s.service.simulatePingFailures.Store(1)
	defer func() {
		s.service.simulatePingFailures.Store(0)
	}()

	_, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: "Ciao mamma guarda come mi diverto"})
	s.Require().Error(err, "Ping should return a simulated failure")
	s.Require().ErrorContains(err, "Simulated failure")
}

func (s *proxyTestSuite) setupResiliency() {
	timeout := 500 * time.Millisecond
	rc := &retry.Config{
		Policy:     retry.PolicyConstant,
		Duration:   200 * time.Millisecond,
		MaxRetries: 3,
	}
	s.policyDef = resiliency.NewPolicyDefinition(testLogger, "test", timeout, rc, nil)
}

func (s *proxyTestSuite) TestResiliencyUnary() {
	const message = "Ciao mamma guarda come mi diverto"

	// Set the resiliency policy and the number of simulated failures in a row
	s.setupResiliency()
	defer func() {
		s.policyDef = nil
	}()

	s.T().Run("retries", func(t *testing.T) {
		s.service.simulatePingFailures.Store(2)
		defer func() {
			s.service.simulatePingFailures.Store(0)
		}()

		setupMetrics(s)

		ctx, cancel := s.ctx()
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(diag.GRPCProxyAppIDKey, testAppID))

		// Reset callCount before this test
		s.service.pingCallCount.Store(0)

		res, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: message})
		require.NoError(t, err, "Ping should succeed after retrying")
		require.NotNil(t, res, "Response should not be nil")
		require.Equal(t, int32(3), s.service.pingCallCount.Load())
		require.Equal(t, message, res.GetValue())

		assertRequestSentMetrics(t, "unary", 3, nil)

		rows, err := view.RetrieveData(serviceInvocationResponseRecvName)
		require.NoError(t, err)
		assert.Len(t, rows, 2)
		// 2 Ping failures
		assert.Equal(t, int64(2), diag.GetValueForObservationWithTagSet(
			rows, map[tag.Tag]bool{diag.NewTag("status", strconv.Itoa(int(codes.Internal))): true}))
		// 1 success
		assert.Equal(t, int64(1), diag.GetValueForObservationWithTagSet(
			rows, map[tag.Tag]bool{diag.NewTag("status", strconv.Itoa(int(codes.OK))): true}))
	})

	s.T().Run("timeouts", func(t *testing.T) {
		// The delay is longer than the timeout set in the resiliency policy (500ms)
		s.service.simulateDelay.Store(800)
		defer func() {
			s.service.simulateDelay.Store(0)
		}()

		// Reset callCount before this test
		s.service.pingCallCount.Store(0)

		setupMetrics(s)

		ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(diag.GRPCProxyAppIDKey, testAppID))

		_, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: message})
		require.Error(t, err, "Ping should fail due to timeouts")
		require.Equal(t, int32(4), s.service.pingCallCount.Load())

		grpcStatus, ok := status.FromError(err)
		s.Require().True(ok, "Error should have a gRPC status code")
		s.Require().Equal(codes.DeadlineExceeded, grpcStatus.Code())

		// Sleep for 500ms before returning to allow all timed-out goroutines to catch up with the timeouts
		time.Sleep(500 * time.Millisecond)

		assertRequestSentMetrics(t, "unary", 4, nil)
		assertResponseReceiveMetricsSameCode(t, "unary", codes.DeadlineExceeded, 4)
	})

	s.T().Run("multiple threads", func(t *testing.T) {
		// Reset callCount before this test
		s.service.pingCallCount.Store(0)

		// Simulate random failures during this test
		// Note that because failures are simulated based on the counter, they're not really "random", although they impact "random" operations since they're parallelized
		s.service.simulateRandomFailures.Store(true)
		defer func() {
			s.service.simulateRandomFailures.Store(false)
		}()

		setupMetrics(s)

		numGoroutines := 10
		numOperations := 10

		wg := sync.WaitGroup{}
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(i int) {
				for j := 0; j < numOperations; j++ {
					pingMsg := fmt.Sprintf("%d:%d", i, j)
					ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(diag.GRPCProxyAppIDKey, testAppID))
					res, err := s.testClient.Ping(ctx, &pb.PingRequest{Value: pingMsg})
					require.NoErrorf(t, err, "Ping should succeed for operation %d:%d", i, j)
					require.NotNilf(t, res, "Response should not be nil for operation %d:%d", i, j)
					require.Equalf(t, pingMsg, res.GetValue(), "Value should match for operation %d:%d", i, j)
				}
				wg.Done()
			}(i)
		}

		ch := make(chan struct{})
		go func() {
			wg.Wait()
			close(ch)
		}()
		select {
		case <-time.After(time.Second * 10):
			s.Fail("Timed out waiting for proxy to return.")
		case <-ch:
		}

		// Sleep for 500ms before returning to allow all timed-out goroutines to catch up with the timeouts
		time.Sleep(500 * time.Millisecond)

		assertRequestSentMetrics(t, "unary", int64(numGoroutines*numOperations), assert.GreaterOrEqual)
	})
}

func assertResponseReceiveMetricsSameCode(t *testing.T, requestType string, code codes.Code, expected int64) []*view.Row {
	t.Helper()
	rows, err := view.RetrieveData(serviceInvocationResponseRecvName)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	count := diag.GetValueForObservationWithTagSet(
		rows, map[tag.Tag]bool{
			diag.NewTag("status", strconv.Itoa(int(code))): true,
			diag.NewTag("type", requestType):               true,
		})
	assert.Equal(t, expected, count)
	return rows
}

func assertRequestSentMetrics(t *testing.T, requestType string, requestsSentExpected int64, assertEqualFn func(t assert.TestingT, e1 interface{}, e2 interface{}, msgAndArgs ...interface{}) bool) []*view.Row {
	t.Helper()
	rows, err := view.RetrieveData(serviceInvocationRequestSentName)
	require.NoError(t, err)
	assert.Len(t, rows, 1)
	requestsSent := diag.GetValueForObservationWithTagSet(
		rows, map[tag.Tag]bool{diag.NewTag("type", requestType): true})

	if assertEqualFn == nil {
		assertEqualFn = assert.Equal
	}
	assertEqualFn(t, requestsSent, requestsSentExpected)
	return rows
}

func (s *proxyTestSuite) TestResiliencyStreaming() {
	// Set the resiliency policy
	s.setupResiliency()
	defer func() {
		s.policyDef = nil
	}()

	s.T().Run("retries are not allowed", func(t *testing.T) {
		// We're purposely not setting dapr-stream=true in this context because we want to simulate the failure when the RPC is not marked as streaming
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
			diag.GRPCProxyAppIDKey, "test",
			"dapr-test", t.Name(),
		))

		// Invoke the stream
		stream, err := s.testClient.PingStream(ctx)
		require.NoError(t, err, "PingStream request should be successful")

		// First message should succeed
		err = stream.Send(&pb.PingRequest{Value: "1"})
		require.NoError(t, err, "First message should be sent")
		res, err := stream.Recv()
		require.NoError(t, err, "First response should be received")
		require.NotNil(t, res)

		// Second message should fail
		s.service.expectPingStreamError.Store(true)
		defer func() {
			s.service.expectPingStreamError.Store(false)
		}()
		err = stream.SendMsg(&pb.PingRequest{Value: "2"})
		require.NoError(t, err, "Second message should be sent")
		_, err = stream.Recv()
		require.Error(t, err, "Second Recv should fail with error")

		grpcStatus, ok := status.FromError(err)
		require.True(t, ok, "Error should have a gRPC status code")
		require.Equal(t, codes.FailedPrecondition, grpcStatus.Code())
		require.ErrorIs(t, err, errRetryOnStreamingRPC)
	})

	s.T().Run("timeouts do not apply after initial handshake", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		setupMetrics(s)

		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
			diag.GRPCProxyAppIDKey, testAppID,
			StreamMetadataKey, "1",
			"dapr-test", t.Name(),
		))

		// Set a delay of 600ms, which is longer than the timeout set in the resiliency policy (500ms)
		s.service.simulateDelay.Store(600)
		defer func() {
			s.service.simulateDelay.Store(0)
		}()

		// Invoke the stream
		start := time.Now()
		stream, err := s.testClient.PingStream(ctx)
		require.NoError(t, err, "PingStream request should be successful")

		// Send and receive 2 messages
		for i := 0; i < 2; i++ {
			innerErr := stream.Send(&pb.PingRequest{Value: strconv.Itoa(i)})
			require.NoError(t, innerErr, "Message should be sent")
			res, innerErr := stream.Recv()
			require.NoError(t, innerErr, "Response should be received")
			require.NotNil(t, res)
		}

		// At least 1200ms (2 * 600ms) should have passed
		require.GreaterOrEqual(t, time.Since(start), 1200*time.Millisecond)

		s.Require().NoError(stream.CloseSend(), "no error on close send")
		_, err = stream.Recv()
		s.Require().ErrorIs(err, io.EOF, "stream should close with io.EOF, meaining OK")

		assertRequestSentMetrics(t, "streaming", 1, nil)
		rows, err := view.RetrieveData(serviceInvocationResponseRecvName)
		require.NoError(t, err)
		assert.Empty(t, rows) // no error so no response metric
	})

	s.T().Run("simulate connection failures with retry", func(t *testing.T) {
		// using 3 connection failures as each connection retry actually executes 2 connection attempts
		s.service.simulateConnectionFailures.Store(3)
		defer func() {
			s.service.simulateConnectionFailures.Store(0)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		setupMetrics(s)

		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
			diag.GRPCProxyAppIDKey, testAppID,
			StreamMetadataKey, "1",
			"dapr-test", t.Name(),
		))
		// Invoke the stream
		stream, err := s.testClient.PingStream(ctx)
		require.NoError(t, err, "PingStream request should be successful")

		// Send and receive 2 messages
		for i := 0; i < 2; i++ {
			innerErr := stream.Send(&pb.PingRequest{Value: strconv.Itoa(i)})
			require.NoError(t, innerErr, "Message should be sent")
			res, innerErr := stream.Recv()
			require.NoError(t, innerErr, "Response should be received")
			require.NotNil(t, res)
		}

		s.Require().NoError(stream.CloseSend(), "no error on close send")
		_, err = stream.Recv()
		s.Require().ErrorIs(err, io.EOF, "stream should close with io.EOF, meaining OK")

		assertRequestSentMetrics(t, "streaming", 2, nil)
		assertResponseReceiveMetricsSameCode(t, "streaming", codes.Unavailable, 1)
	})
}

func setupMetrics(s *proxyTestSuite) {
	s.T().Helper()
	metricsCleanup()
	s.Require().NoError(diag.DefaultMonitoring.Init(testAppID))
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
	s.Require().NoError(err, "must not error while starting serverListener")

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
			grpc.WithStreamInterceptor(createGrpcStreamingChaosInterceptor(s.service.simulateConnectionFailures, codes.Unavailable)),
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
	s.Require().NotNil(pc, "proxy codec must be registered")
	s.Require().NotNil(dc, "default codec must be registered")

	s.proxyListener, err = net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err, "must be able to allocate a port for proxyListener")
	s.serverListener, err = net.Listen("tcp", "127.0.0.1:0")
	s.Require().NoError(err, "must be able to allocate a port for serverListener")

	grpclog.SetLoggerV2(testingLog{s.T()})

	s.service = &assertingService{
		t:                          s.T(),
		expectPingStreamError:      &atomic.Bool{},
		simulatePingFailures:       &atomic.Int32{},
		simulateConnectionFailures: &atomic.Int32{},
		pingCallCount:              &atomic.Int32{},
		simulateDelay:              &atomic.Int32{},
		simulateRandomFailures:     &atomic.Bool{},
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
	getPolicyFn := func(appID, methodName string) *resiliency.PolicyDefinition {
		return s.policyDef
	}
	th := TransparentHandler(director, getPolicyFn,
		func(ctx context.Context, address, id, namespace string, customOpts ...grpc.DialOption) (*grpc.ClientConn, func(destroy bool), error) {
			return s.getServerClientConn()
		},
		4,
	)
	s.proxy = grpc.NewServer(
		grpc.UnknownServiceHandler(th),
	)
	// Ping handler is handled as an explicit registration and not as a TransparentHandler.
	RegisterService(s.proxy, director, getPolicyFn,
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
		s.proxyListener.Addr().String(), // DO NOT USE "localhost" as it does not resolve to loopback in some environments.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.CallContentSubtype((&codec.Proxy{}).Name())),
	)
	s.Require().NoError(err, "must not error on deferred client Dial")
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
	codec.Register()
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

// createGrpcStreamingChaosInterceptor creates a gRPC interceptor that will return the given error code
// it does not do any post-processing chaos
func createGrpcStreamingChaosInterceptor(interruptionsCounter *atomic.Int32, code codes.Code) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if interruptionsCounter.Load() > 0 {
			interruptionsCounter.Add(-1)
			return nil, status.Errorf(code, "chaos monkey strikes again")
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}
