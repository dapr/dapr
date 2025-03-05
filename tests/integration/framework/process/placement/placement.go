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
	"sync"
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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/metrics"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Placement struct {
	exec  process.Interface
	ports *ports.Ports

	id                  string
	port                int
	healthzPort         int
	metricsPort         int
	initialCluster      string
	initialClusterPorts []int

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Placement {
	t.Helper()

	uid, err := uuid.NewUUID()
	require.NoError(t, err)

	fp := ports.Reserve(t, 4)
	port := fp.Port(t)
	opts := options{
		id:                  uid.String(),
		logLevel:            "debug",
		port:                fp.Port(t),
		healthzPort:         fp.Port(t),
		metricsPort:         fp.Port(t),
		initialCluster:      uid.String() + "=127.0.0.1:" + strconv.Itoa(port),
		initialClusterPorts: []int{port},
		metadataEnabled:     false,
		namespace:           "default",
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--id=" + opts.id,
		"--port=" + strconv.Itoa(opts.port),
		"--listen-address=127.0.0.1",
		"--healthz-port=" + strconv.Itoa(opts.healthzPort),
		"--healthz-listen-address=127.0.0.1",
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--metrics-listen-address=127.0.0.1",
		"--initial-cluster=" + opts.initialCluster,
		"--tls-enabled=" + strconv.FormatBool(opts.tlsEnabled),
		"--metadata-enabled=" + strconv.FormatBool(opts.metadataEnabled),
	}
	if opts.maxAPILevel != nil {
		args = append(args, "--max-api-level="+strconv.Itoa(*opts.maxAPILevel))
	}
	if opts.minAPILevel != nil {
		args = append(args, "--min-api-level="+strconv.Itoa(*opts.minAPILevel))
	}
	if opts.sentryAddress != nil {
		args = append(args, "--sentry-address="+*opts.sentryAddress)
	}
	if opts.trustAnchorsFile != nil {
		args = append(args, "--trust-anchors-file="+*opts.trustAnchorsFile)
	}
	if opts.trustDomain != nil {
		args = append(args, "--trust-domain="+*opts.trustDomain)
	}
	if opts.mode != nil {
		args = append(args, "--mode="+*opts.mode)
	}

	return &Placement{
		exec: exec.New(t, binary.EnvValue("placement"), args,
			append(opts.execOpts, exec.WithEnvVars(t,
				"NAMESPACE", opts.namespace,
			))...,
		),
		ports:               fp,
		id:                  opts.id,
		port:                opts.port,
		healthzPort:         opts.healthzPort,
		metricsPort:         opts.metricsPort,
		initialCluster:      opts.initialCluster,
		initialClusterPorts: opts.initialClusterPorts,
	}
}

func (p *Placement) Run(t *testing.T, ctx context.Context) {
	p.runOnce.Do(func() {
		p.ports.Free(t)
		p.exec.Run(t, ctx)
	})
}

func (p *Placement) Cleanup(t *testing.T) {
	p.cleanupOnce.Do(func() {
		p.exec.Cleanup(t)
	})
}

func (p *Placement) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()

	client := client.HTTP(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", p.healthzPort), nil)
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, respErr := client.Do(req)
		if assert.NoError(c, respErr) {
			defer resp.Body.Close()
			assert.Equal(c, http.StatusOK, resp.StatusCode)
		}
	}, time.Second*25, 10*time.Millisecond)
}

func (p *Placement) ID() string {
	return p.id
}

func (p *Placement) Port() int {
	return p.port
}

func (p *Placement) ipPort(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}

func (p *Placement) Address() string {
	return p.ipPort(p.port)
}

func (p *Placement) HealthzPort() int {
	return p.healthzPort
}

// Metrics returns a subset of metrics scraped from the metrics endpoint
func (p *Placement) Metrics(t assert.TestingT, ctx context.Context) *metrics.Metrics {
	return metrics.New(t, ctx, fmt.Sprintf("http://%s/metrics", p.MetricsAddress()))
}

func (p *Placement) MetricsAddress() string {
	return p.ipPort(p.MetricsPort())
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

// RegisterHost Registers a host with the placement service
func (p *Placement) RegisterHost(t *testing.T, parentCtx context.Context, msg *placementv1pb.Host) chan *placementv1pb.PlacementTables {
	//nolint:staticcheck
	conn, err := grpc.DialContext(parentCtx, p.Address(),
		grpc.WithBlock(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	client := placementv1pb.NewPlacementClient(conn)

	var stream placementv1pb.Placement_ReportDaprStatusClient
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		stream, err = client.ReportDaprStatus(parentCtx)
		if !assert.NoError(c, err) {
			return
		}

		if !assert.NoError(c, stream.Send(msg)) {
			_ = stream.CloseSend()
			return
		}

		_, err = stream.Recv()
		if !assert.NoError(c, err) {
			_ = stream.CloseSend()
			return
		}
	}, time.Second*15, time.Millisecond*10)

	doneCh := make(chan error)
	placementUpdateCh := make(chan *placementv1pb.PlacementTables)
	ctx, cancel := context.WithCancel(parentCtx)

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-doneCh:
			if !errors.Is(err, io.EOF) {
				require.NoError(t, err)
			}
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
			case <-time.After(500 * time.Millisecond):
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
				return
			}

			if in.GetOperation() == "update" {
				tables := in.GetTables()
				assert.NotEmptyf(t, tables, "Placement table is empty")

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
func (p *Placement) AssertRegisterHostFails(t *testing.T, ctx context.Context, apiLevel uint32) {
	msg := &placementv1pb.Host{
		Name:     "myapp-fail",
		Port:     1234,
		Entities: []string{"someactor"},
		Id:       "myapp",
		ApiLevel: apiLevel,
	}
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, p.Address(),
		grpc.WithBlock(), //nolint:staticcheck
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
