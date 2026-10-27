/*
Copyright 2026 The Dapr Authors
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
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
)

type staleFakeClient struct {
	internalv1pb.ServiceInvocationClient
	// gate, when non-nil, is waited on by the first CallLocalStream call.
	gate  chan struct{}
	mu    sync.Mutex
	calls int
	sent  map[int]*bytes.Buffer
}

func (c *staleFakeClient) CallLocalStream(ctx context.Context, _ ...grpc.CallOption) (internalv1pb.ServiceInvocation_CallLocalStreamClient, error) {
	c.mu.Lock()
	c.calls++
	n := c.calls
	buf := &bytes.Buffer{}
	c.sent[n] = buf
	c.mu.Unlock()
	if n == 1 && c.gate != nil {
		<-c.gate
	}
	return &staleFakeStream{buf: buf, mu: &c.mu}, nil
}

type staleFakeStream struct {
	grpc.ClientStream
	buf  *bytes.Buffer
	mu   *sync.Mutex
	recv int
}

func (s *staleFakeStream) Send(m *internalv1pb.InternalInvokeRequestStream) error {
	return s.SendMsg(m)
}

func (s *staleFakeStream) SendMsg(m any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := m.(*internalv1pb.InternalInvokeRequestStream).GetPayload(); p != nil {
		s.buf.Write(p.GetData())
	}
	return nil
}
func (s *staleFakeStream) CloseSend() error { return nil }
func (s *staleFakeStream) Recv() (*internalv1pb.InternalInvokeResponseStream, error) {
	return nil, io.EOF
}

func (s *staleFakeStream) RecvMsg(m any) error {
	s.recv++
	if s.recv > 1 {
		return io.EOF
	}
	m.(*internalv1pb.InternalInvokeResponseStream).Response = &internalv1pb.InternalInvokeResponse{
		Status: &internalv1pb.Status{Code: 0},
	}
	return nil
}

// A resiliency attempt that timed out is not waited for. When it reaches RawData() after the next attempt sent the
// body, and returns without reading because its context is canceled, the attempt after that must still send the body.
func TestInvokeRemoteStreamStaleAttemptKeepsBody(t *testing.T) {
	body := strings.Repeat("x", 61)
	fc := &staleFakeClient{gate: make(chan struct{}), sent: map[int]*bytes.Buffer{}}
	d := &directMessaging{}

	req := invokev1.NewInvokeMethodRequest("method").
		WithRawData(io.NopCloser(strings.NewReader(body))).
		WithReplay(true)
	defer req.Close()

	noop := func(bool) {}

	// Attempt 1: blocks inside CallLocalStream, before req.RawData().
	ctx1, cancel1 := context.WithCancel(t.Context())
	done1 := make(chan struct{})
	go func() {
		defer close(done1)
		_, _, _ = d.invokeRemoteStream(ctx1, fc, req, "app", nil, noop)
	}()
	// Wait until attempt 1 is inside CallLocalStream.
	require.Eventually(t, func() bool { fc.mu.Lock(); defer fc.mu.Unlock(); return fc.calls == 1 }, 5e9, 1e6)

	// Timeout fires for attempt 1; attempt 2 runs to completion.
	cancel1()
	resp, _, err := d.invokeRemoteStream(t.Context(), fc, req, "app", nil, noop)
	require.NoError(t, err)
	_ = resp.Close()
	fc.mu.Lock()
	require.Equal(t, body, fc.sent[2].String(), "attempt 2 should send the body")
	fc.mu.Unlock()

	// Stale attempt 1 now proceeds.
	close(fc.gate)
	<-done1

	// Attempt 3.
	resp, _, err = d.invokeRemoteStream(t.Context(), fc, req, "app", nil, noop)
	require.NoError(t, err)
	_ = resp.Close()
	fc.mu.Lock()
	defer fc.mu.Unlock()
	assert.Equal(t, body, fc.sent[3].String(), "attempt 3 should send the body")
}
