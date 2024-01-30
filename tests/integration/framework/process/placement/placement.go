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

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Placement struct {
	exec     process.Interface
	freeport *util.FreePort
	running  atomic.Bool

	id                  string
	port                int
	healthzPort         int
	metricsPort         int
	initialCluster      string
	initialClusterPorts []int
}

func New(t *testing.T, fopts ...Option) *Placement {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	fp := util.ReservePorts(t, 4)
	opts := options{
		id:                  uid.String(),
		logLevel:            "info",
		port:                fp.Port(t, 0),
		healthzPort:         fp.Port(t, 1),
		metricsPort:         fp.Port(t, 2),
		initialCluster:      uid.String() + "=localhost:" + strconv.Itoa(fp.Port(t, 3)),
		initialClusterPorts: []int{fp.Port(t, 3)},
		maxAPILevel:         -1,
		minAPILevel:         0,
		metadataEnabled:     false,
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--port=" + strconv.Itoa(opts.port),
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--initial-cluster=" + opts.initialCluster,
		"--tls-enabled=" + strconv.FormatBool(opts.tlsEnabled),
		"--max-api-level=" + strconv.Itoa(opts.maxAPILevel),
		"--min-api-level=" + strconv.Itoa(opts.minAPILevel),
		"--metadata-enabled=" + strconv.FormatBool(opts.metadataEnabled),
	}
	if opts.sentryAddress != nil {
		args = append(args, "--sentry-address="+*opts.sentryAddress)
	}
	if opts.trustAnchorsFile != nil {
		args = append(args, "--trust-anchors-file="+*opts.trustAnchorsFile)
	}

	return &Placement{
		exec:                exec.New(t, binary.EnvValue("placement"), args, opts.execOpts...),
		freeport:            fp,
		id:                  opts.id,
		port:                opts.port,
		healthzPort:         opts.healthzPort,
		metricsPort:         opts.metricsPort,
		initialCluster:      opts.initialCluster,
		initialClusterPorts: opts.initialClusterPorts,
	}
}

func (p *Placement) Run(t *testing.T, ctx context.Context) {
	if !p.running.CompareAndSwap(false, true) {
		t.Fatal("Process is already running")
	}

	p.freeport.Free(t)
	p.exec.Run(t, ctx)
}

func (p *Placement) Cleanup(t *testing.T) {
	if !p.running.CompareAndSwap(true, false) {
		return
	}

	p.exec.Cleanup(t)
}

func (p *Placement) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	assert.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://localhost:%d/healthz", p.healthzPort), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusOK == resp.StatusCode
	}, time.Second*5, 100*time.Millisecond)
}

func (p *Placement) ID() string {
	return p.id
}

func (p *Placement) Port() int {
	return p.port
}

func (p *Placement) Address() string {
	return "localhost:" + strconv.Itoa(p.port)
}

func (p *Placement) HealthzPort() int {
	return p.healthzPort
}

func (p *Placement) MetricsPort() int {
	return p.metricsPort
}

func (p *Placement) InitialCluster() string {
	return p.initialCluster
}

func (p *Placement) InitialClusterPorts() []int {
	return p.initialClusterPorts
}

func (p *Placement) CurrentActorsAPILevel() int {
	return 20 // Defined in pkg/actors/internal/api_level.go
}

func (p *Placement) EstablishConn(ctx context.Context) (*grpc.ClientConn, error) {
	return grpc.DialContext(ctx, "localhost:"+strconv.Itoa(p.port),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(grpcinsecure.NewCredentials()),
	)
}

func RegisterHost(t *testing.T, ctx context.Context, conn *grpc.ClientConn, msg *placementv1pb.Host, placementMessage chan any, stopCh chan struct{}) {
	// Establish a stream and send the initial heartbeat
	// We need to retry here because this will fail until the instance of placement (the only one) acquires leadership
	var placementStream placementv1pb.Placement_ReportDaprStatusClient

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		client := placementv1pb.NewPlacementClient(conn)

		stream, rErr := client.ReportDaprStatus(ctx)
		//nolint:testifylint
		if !assert.NoError(c, rErr) {
			return
		}

		rErr = stream.Send(msg)
		//nolint:testifylint
		if !assert.NoError(c, rErr) {
			_ = stream.CloseSend()
			return
		}

		// Receive the first message (which can't be an "update" one anyways) to ensure the connection is ready
		_, rErr = stream.Recv()
		//nolint:testifylint
		if !assert.NoError(c, rErr) {
			_ = stream.CloseSend()
			return
		}

		placementStream = stream
	}, time.Second*15, time.Millisecond*500)

	// Send messages every second
	go func() {
		for {
			select {
			case <-ctx.Done():
				placementStream.CloseSend()
				return
			case <-stopCh:
				// Disconnect when there's a signal on stopCh
				placementStream.CloseSend()
				return
			case <-time.After(time.Second):
				placementStream.Send(msg)
			}
		}
	}()

	// Collect all API levels
	go func() {
		for {
			in, rerr := placementStream.Recv()
			if rerr != nil {
				if ctx.Err() != nil || errors.Is(rerr, context.Canceled) || errors.Is(rerr, io.EOF) || status.Code(rerr) == codes.Canceled {
					// Stream ended
					placementMessage <- nil
					return
				}
				placementMessage <- fmt.Errorf("error from placement: %w", rerr)
				return
			}
			if in.GetOperation() == "update" {
				placementMessage <- in.GetTables()
			}
		}
	}()
}

// Expect the registration to fail with FailedPrecondition.
func RegisterHostFailing(t *testing.T, ctx context.Context, conn *grpc.ClientConn, apiLevel int) {
	msg := &placementv1pb.Host{
		Name:     "myapp-fail",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: uint32(apiLevel),
	}

	client := placementv1pb.NewPlacementClient(conn)
	stream, err := client.ReportDaprStatus(ctx)
	require.NoError(t, err, "failed to establish stream")

	err = stream.Send(msg)
	require.NoError(t, err, "failed to send message")

	// Should fail here
	_, err = stream.Recv()
	require.Error(t, err)
	require.Equalf(t, codes.FailedPrecondition, status.Code(err), "error was: %v", err)
}

// Checks the API level reported in the state table matched.
func CheckAPILevelInState(t require.TestingT, client *http.Client, port int, expectAPILevel int) (tableVersion int) {
	res, err := client.Get(fmt.Sprintf("http://localhost:%d/placement/state", port))
	require.NoError(t, err)
	defer res.Body.Close()

	stateRes := struct {
		APILevel     int `json:"apiLevel"`
		TableVersion int `json:"tableVersion"`
	}{}
	err = json.NewDecoder(res.Body).Decode(&stateRes)
	require.NoError(t, err)

	assert.Equal(t, expectAPILevel, stateRes.APILevel)

	return stateRes.TableVersion
}
