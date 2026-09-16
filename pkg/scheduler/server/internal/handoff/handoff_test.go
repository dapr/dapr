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

package handoff

import (
	"context"
	"errors"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security/fake"
)

func TestKubernetesPresence(t *testing.T) {
	t.Parallel()

	h := New(Options{})

	assert.False(t, h.PlacementPresent())

	h.SetKubernetesPresence(true)
	assert.True(t, h.PlacementPresent())

	h.SetKubernetesPresence(false)
	assert.False(t, h.PlacementPresent())
}

func TestPresenceResetsAdvertised(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	h.LatchAdvertised()
	require.True(t, h.Advertised())

	// A reappearing placement service means the next cutover waits for a
	// capable sidecar again.
	h.SetKubernetesPresence(true)
	assert.False(t, h.Advertised())

	h.LatchAdvertised()
	h.SetKubernetesPresence(true)
	assert.True(t, h.Advertised(), "an unchanged presence keeps the latch")

	h.SetKubernetesPresence(false)
	assert.True(t, h.Advertised(), "an absence keeps the latch")

	h.SetKubernetesPresence(true)
	assert.False(t, h.Advertised())
}

func TestDetectionResetsAdvertised(t *testing.T) {
	t.Parallel()

	h := New(Options{PlacementDNSName: "dapr-placement-server"})
	resolved := true
	h.lookupHost = func(context.Context, string) ([]string, error) {
		if resolved {
			return []string{"10.0.0.1"}, nil
		}
		return nil, assert.AnError
	}

	h.refreshDetection(t.Context())
	h.LatchAdvertised()
	require.True(t, h.Advertised())

	h.refreshDetection(t.Context())
	assert.True(t, h.Advertised(), "an unchanged sighting keeps the latch")

	resolved = false
	for range absenceConfirmations {
		h.refreshDetection(t.Context())
		assert.True(t, h.Advertised(), "an absence keeps the latch")
	}

	resolved = true
	h.refreshDetection(t.Context())
	assert.False(t, h.Advertised())
}

func TestDetectionSighting(t *testing.T) {
	t.Parallel()

	h := New(Options{PlacementDNSName: "dapr-placement-server"})
	resolved := false
	h.lookupHost = func(context.Context, string) ([]string, error) {
		if resolved {
			return []string{"10.0.0.1"}, nil
		}
		return nil, assert.AnError
	}

	h.refreshDetection(t.Context())
	assert.False(t, h.PlacementPresent())

	// A placement service too old to know about scheduler placement must
	// still withhold the advertisement.
	resolved = true
	h.refreshDetection(t.Context())
	assert.True(t, h.PlacementPresent())

	// One sightless refresh can be a probe running out of time or a
	// placement restart: absence needs consecutive confirmations.
	resolved = false
	for range absenceConfirmations - 1 {
		h.refreshDetection(t.Context())
		assert.True(t, h.PlacementPresent())
		assert.True(t, h.confirmingAbsence())
	}
	h.refreshDetection(t.Context())
	assert.False(t, h.PlacementPresent())
	assert.False(t, h.confirmingAbsence())

	// A sighting mid-confirmation keeps presence with no confirmation
	// pending.
	resolved = true
	h.refreshDetection(t.Context())
	require.True(t, h.PlacementPresent())
	resolved = false
	h.refreshDetection(t.Context())
	resolved = true
	h.refreshDetection(t.Context())
	assert.True(t, h.PlacementPresent())
	assert.False(t, h.confirmingAbsence())
}

func TestPendingDetectionIsPresence(t *testing.T) {
	t.Parallel()

	h := New(Options{})

	// A just-reported placement address is treated as a present placement
	// service until the refresh completes.
	h.RequestDetection()
	assert.True(t, h.PlacementPresent())

	h.refreshDetection(t.Context())
	assert.False(t, h.PlacementPresent())
}

func TestKubernetesAbsenceRequestsRefresh(t *testing.T) {
	t.Parallel()

	h := New(Options{})

	h.SetKubernetesPresence(true)
	select {
	case <-h.detectCh:
		t.Fatal("presence must not request a refresh")
	default:
	}

	// The pods vanishing starts the absence confirmations right away rather
	// than on the next idle tick.
	h.SetKubernetesPresence(false)
	select {
	case <-h.detectCh:
	default:
		t.Fatal("absence must request a refresh")
	}
}

func TestPendingDetectionKeepsAdvertisement(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	h.LatchAdvertised()

	// A reconnecting sidecar re-reports its addresses: the in-flight
	// presumption must not withdraw the standing advertisement.
	h.RequestDetection()
	assert.False(t, h.PlacementPresent())

	h.refreshDetection(t.Context())
	assert.False(t, h.PlacementPresent())
}

func TestLocalCapabilities(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	assert.False(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.False(t, h.AnySchedulerPlacementCapableSidecars())

	h.SetLocalCapabilities(true, true)
	assert.True(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())

	h.SetLocalCapabilities(false, true)
	assert.False(t, h.AnySchedulerPlacementIncapableSidecars())
	assert.True(t, h.AnySchedulerPlacementCapableSidecars())
}

func TestReady(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	assert.False(t, h.Ready(), "not ready before the first detection")

	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() { errCh <- h.Run(ctx) }()

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, h.Ready())
	}, 5e9, 1e7)

	cancel()
	require.Error(t, <-errCh)
}

func TestOnChange(t *testing.T) {
	t.Parallel()

	h := New(Options{})
	fired := 0
	h.SetOnChange(func() { fired++ })

	h.SetKubernetesPresence(true)
	h.SetKubernetesPresence(true)
	h.SetKubernetesPresence(false)
	h.SetLocalCapabilities(true, false)
	assert.Equal(t, 3, fired, "an unchanged presence does not fire")
}

func TestProbeAddressRequiresPlacementProtocol(t *testing.T) {
	t.Parallel()

	newServer := func(t *testing.T, placement bool) string {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		srv := grpc.NewServer()
		if placement {
			v1pb.RegisterPlacementServer(srv, &fakePlacementServer{})
		}
		go srv.Serve(lis)
		t.Cleanup(srv.Stop)
		return lis.Addr().String()
	}

	h := New(Options{Security: fake.New()})
	id, err := spiffeid.FromSegments(spiffeid.RequireTrustDomainFromString("public"), "ns", "default", "dapr-placement")
	require.NoError(t, err)

	assert.True(t, h.probeAddress(t.Context(), newServer(t, true), id))
	assert.False(t, h.probeAddress(t.Context(), newServer(t, false), id),
		"a gRPC server which does not speak the placement protocol is not a placement service")

	// A protocol check running out of time is no answer, not a sighting.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	v1pb.RegisterPlacementServer(srv, &hangingPlacementServer{})
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	assert.False(t, h.probeAddress(t.Context(), lis.Addr().String(), id),
		"a hanging protocol check is not a sighting")
}

type hangingPlacementServer struct {
	v1pb.UnimplementedPlacementServer
}

func (h *hangingPlacementServer) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	<-stream.Context().Done()
	return stream.Context().Err()
}

type fakePlacementServer struct {
	v1pb.UnimplementedPlacementServer
}

func (f *fakePlacementServer) ReportDaprStatus(stream v1pb.Placement_ReportDaprStatusServer) error {
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func TestReadyFiresOnChange(t *testing.T) {
	t.Parallel()

	h := New(Options{Security: fake.New()})
	var fired atomic.Int64
	h.SetOnChange(func() { fired.Add(1) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.Error(t, h.Run(ctx))
	assert.True(t, h.Ready())
	assert.Positive(t, fired.Load())
}

func TestProbeReportedAddresses(t *testing.T) {
	t.Parallel()

	// unresponsive accepts connections but never answers, so a probe of the
	// address runs until its deadline.
	unresponsive := func(t *testing.T) string {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		t.Cleanup(func() { lis.Close() })
		go func() {
			for {
				conn, aerr := lis.Accept()
				if aerr != nil {
					return
				}
				go func() {
					//nolint:errcheck
					io.Copy(io.Discard, conn)
					conn.Close()
				}()
			}
		}()
		return lis.Addr().String()
	}

	t.Run("unresponsive addresses stay absent within the budget", func(t *testing.T) {
		h := New(Options{Security: fake.New()})
		addrs := make([]string, probeConcurrency*2)
		for i := range addrs {
			addrs[i] = unresponsive(t)
		}
		h.SetPlacementAddresses(func() []string { return addrs })

		start := time.Now()
		assert.False(t, h.probeReportedAddresses(t.Context()))
		assert.Less(t, time.Since(start), probeBudget+time.Second*2)
	})

	t.Run("in-flight probes never exceed the concurrency cap", func(t *testing.T) {
		h := New(Options{Security: fake.New()})
		var inflight, peak, calls atomic.Int64
		h.probe = func(context.Context, string, spiffeid.ID) bool {
			cur := inflight.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			defer inflight.Add(-1)
			calls.Add(1)
			time.Sleep(time.Millisecond * 20)
			return false
		}

		addrs := make([]string, probeConcurrency*3)
		for i := range addrs {
			addrs[i] = "127.0.0.1:1"
		}
		h.SetPlacementAddresses(func() []string { return addrs })

		assert.False(t, h.probeReportedAddresses(t.Context()))
		assert.Equal(t, int64(len(addrs)), calls.Load())
		assert.LessOrEqual(t, peak.Load(), int64(probeConcurrency))
	})

	t.Run("one placement service among unresponsive addresses is found before the budget", func(t *testing.T) {
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		srv := grpc.NewServer()
		v1pb.RegisterPlacementServer(srv, &fakePlacementServer{})
		go srv.Serve(lis)
		t.Cleanup(srv.Stop)

		h := New(Options{Security: fake.New()})
		addrs := []string{unresponsive(t), unresponsive(t), unresponsive(t), lis.Addr().String()}
		h.SetPlacementAddresses(func() []string { return addrs })

		start := time.Now()
		assert.True(t, h.probeReportedAddresses(t.Context()))
		assert.Less(t, time.Since(start), probeBudget)
	})

	t.Run("nil source is absent", func(t *testing.T) {
		h := New(Options{Security: fake.New()})
		assert.False(t, h.probeReportedAddresses(t.Context()))
	})
}
