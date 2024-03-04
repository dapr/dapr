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
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
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
		initialCluster:      uid.String() + "=127.0.0.1:" + strconv.Itoa(fp.Port(t, 3)),
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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", p.healthzPort), nil)
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
	return "127.0.0.1:" + strconv.Itoa(p.port)
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

func (p *Placement) RegisterHost(t *testing.T, parentCtx context.Context, msg *placementv1pb.Host) chan *placementv1pb.PlacementTables {
	conn, err := grpc.DialContext(parentCtx, p.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := placementv1pb.NewPlacementClient(conn)

	var stream placementv1pb.Placement_ReportDaprStatusClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err = client.ReportDaprStatus(parentCtx)
		//nolint:testifylint
		if !assert.NoError(c, err) {
			return
		}

		//nolint:testifylint
		if !assert.NoError(c, stream.Send(msg)) {
			_ = stream.CloseSend()
			return
		}

		_, err = stream.Recv()
		//nolint:testifylint
		if !assert.NoError(c, err) {
			_ = stream.CloseSend()
			return
		}
	}, time.Second*15, time.Millisecond*100)

	doneCh := make(chan error)
	placementUpdateCh := make(chan *placementv1pb.PlacementTables)
	ctx, cancel := context.WithCancel(parentCtx)

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-doneCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "timeout waiting for stream to close")
		}
	})

	// Send dapr status messages every second
	go func() {
		for {
			select {
			case <-ctx.Done():
				doneCh <- stream.CloseSend()
				return
			case <-time.After(time.Second):
				if err := stream.Send(msg); err != nil {
					doneCh <- err
					return
				}
			}
		}
	}()

	go func() {
		defer close(placementUpdateCh)
		defer cancel()
		for {
			in, err := stream.Recv()
			if err != nil {
				if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
					return
				}
				require.NoError(t, err)
				return
			}

			if in.GetOperation() == "update" {
				tables := in.GetTables()
				require.NotEmptyf(t, tables, "Placement tables is empty")

				select {
				case placementUpdateCh <- tables:
				case <-ctx.Done():
				}
			}
		}
	}()

	return placementUpdateCh
}

// AssertRegisterHostFails Expect the registration to fail with FailedPrecondition.
func (p *Placement) AssertRegisterHostFails(t *testing.T, ctx context.Context, apiLevel int) {
	msg := &placementv1pb.Host{
		Name:     "myapp-fail",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: uint32(apiLevel),
	}

	conn, err := grpc.DialContext(ctx, p.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

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

// CheckAPILevelInState Checks the API level reported in the state table matched.
func (p *Placement) CheckAPILevelInState(t require.TestingT, client *http.Client, expectedAPILevel int) (tableVersion int) {
	res, err := client.Get(fmt.Sprintf("http://localhost:%d/placement/state", p.HealthzPort()))
	require.NoError(t, err)
	defer res.Body.Close()

	stateRes := struct {
		APILevel     int `json:"apiLevel"`
		TableVersion int `json:"tableVersion"`
	}{}
	err = json.NewDecoder(res.Body).Decode(&stateRes)
	require.NoError(t, err)

	assert.Equal(t, expectedAPILevel, stateRes.APILevel)

	return stateRes.TableVersion
}
