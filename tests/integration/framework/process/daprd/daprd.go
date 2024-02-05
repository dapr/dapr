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

package daprd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/binary"
	"github.com/dapr/dapr/tests/integration/framework/process"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prochttp "github.com/dapr/dapr/tests/integration/framework/process/http"
	"github.com/dapr/dapr/tests/integration/framework/util"
)

type Daprd struct {
	exec     process.Interface
	appHTTP  process.Interface
	freeport *util.FreePort

	appID            string
	namespace        string
	appProtocol      string
	appPort          int
	grpcPort         int
	httpPort         int
	internalGRPCPort int
	publicPort       int
	metricsPort      int
	profilePort      int
}

func New(t *testing.T, fopts ...Option) *Daprd {
	t.Helper()

	uid, err := uuid.NewRandom()
	require.NoError(t, err)

	appHTTP := prochttp.New(t)

	fp := util.ReservePorts(t, 6)
	opts := options{
		appID:            uid.String(),
		appPort:          appHTTP.Port(),
		appProtocol:      "http",
		grpcPort:         fp.Port(t, 0),
		httpPort:         fp.Port(t, 1),
		internalGRPCPort: fp.Port(t, 2),
		publicPort:       fp.Port(t, 3),
		metricsPort:      fp.Port(t, 4),
		profilePort:      fp.Port(t, 5),
		logLevel:         "info",
		mode:             "standalone",
	}

	for _, fopt := range fopts {
		fopt(&opts)
	}

	dir := t.TempDir()
	for i, file := range opts.resourceFiles {
		require.NoError(t, os.WriteFile(filepath.Join(dir, strconv.Itoa(i)+".yaml"), []byte(file), 0o600))
	}

	args := []string{
		"--log-level=" + opts.logLevel,
		"--app-id=" + opts.appID,
		"--app-port=" + strconv.Itoa(opts.appPort),
		"--app-protocol=" + opts.appProtocol,
		"--dapr-grpc-port=" + strconv.Itoa(opts.grpcPort),
		"--dapr-http-port=" + strconv.Itoa(opts.httpPort),
		"--dapr-internal-grpc-port=" + strconv.Itoa(opts.internalGRPCPort),
		"--dapr-public-port=" + strconv.Itoa(opts.publicPort),
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--profile-port=" + strconv.Itoa(opts.profilePort),
		"--enable-app-health-check=" + strconv.FormatBool(opts.appHealthCheck),
		"--app-health-probe-interval=" + strconv.Itoa(opts.appHealthProbeInterval),
		"--app-health-threshold=" + strconv.Itoa(opts.appHealthProbeThreshold),
		"--mode=" + opts.mode,
		"--enable-mtls=" + strconv.FormatBool(opts.enableMTLS),
	}
	if opts.appHealthCheckPath != "" {
		args = append(args, "--app-health-check-path="+opts.appHealthCheckPath)
	}
	if len(opts.resourceFiles) > 0 {
		args = append(args, "--resources-path="+dir)
	}
	for _, dir := range opts.resourceDirs {
		args = append(args, "--resources-path="+dir)
	}
	if len(opts.configs) > 0 {
		for _, c := range opts.configs {
			args = append(args, "--config="+c)
		}
	}
	if len(opts.placementAddresses) > 0 {
		args = append(args, "--placement-host-address="+strings.Join(opts.placementAddresses, ","))
	}
	if len(opts.sentryAddress) > 0 {
		args = append(args, "--sentry-address="+opts.sentryAddress)
	}
	if len(opts.controlPlaneAddress) > 0 {
		args = append(args, "--control-plane-address="+opts.controlPlaneAddress)
	}
	if opts.disableK8sSecretStore != nil {
		args = append(args, "--disable-builtin-k8s-secret-store="+strconv.FormatBool(*opts.disableK8sSecretStore))
	}
	if opts.gracefulShutdownSeconds != nil {
		args = append(args, "--dapr-graceful-shutdown-seconds="+strconv.Itoa(*opts.gracefulShutdownSeconds))
	}
	if opts.blockShutdownDuration != nil {
		args = append(args, "--dapr-block-shutdown-duration="+*opts.blockShutdownDuration)
	}

	ns := "default"
	if opts.namespace != nil {
		ns = *opts.namespace
		opts.execOpts = append(opts.execOpts, exec.WithEnvVars(t, "NAMESPACE", *opts.namespace))
	}

	return &Daprd{
		exec:             exec.New(t, binary.EnvValue("daprd"), args, opts.execOpts...),
		freeport:         fp,
		appHTTP:          appHTTP,
		appID:            opts.appID,
		namespace:        ns,
		appProtocol:      opts.appProtocol,
		appPort:          opts.appPort,
		grpcPort:         opts.grpcPort,
		httpPort:         opts.httpPort,
		internalGRPCPort: opts.internalGRPCPort,
		publicPort:       opts.publicPort,
		metricsPort:      opts.metricsPort,
		profilePort:      opts.profilePort,
	}
}

func (d *Daprd) Run(t *testing.T, ctx context.Context) {
	d.appHTTP.Run(t, ctx)
	d.freeport.Free(t)
	d.exec.Run(t, ctx)
}

func (d *Daprd) Cleanup(t *testing.T) {
	d.exec.Cleanup(t)
	d.appHTTP.Cleanup(t)
}

func (d *Daprd) WaitUntilTCPReady(t *testing.T, ctx context.Context) {
	assert.Eventually(t, func() bool {
		dialer := net.Dialer{Timeout: time.Second}
		net, err := dialer.DialContext(ctx, "tcp", d.HTTPAddress())
		if err != nil {
			return false
		}
		net.Close()
		return true
	}, 10*time.Second, 100*time.Millisecond)
}

func (d *Daprd) WaitUntilRunning(t *testing.T, ctx context.Context) {
	client := util.HTTPClient(t)
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1.0/healthz", d.httpPort), nil)
		if err != nil {
			return false
		}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		return http.StatusNoContent == resp.StatusCode
	}, 10*time.Second, 100*time.Millisecond)
}

func (d *Daprd) WaitUntilAppHealth(t *testing.T, ctx context.Context) {
	switch d.appProtocol {
	case "http":
		client := util.HTTPClient(t)
		assert.Eventually(t, func() bool {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1.0/healthz", d.httpPort), nil)
			if err != nil {
				return false
			}
			resp, err := client.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return http.StatusNoContent == resp.StatusCode
		}, 10*time.Second, 100*time.Millisecond)

	case "grpc":
		assert.Eventually(t, func() bool {
			conn, err := grpc.Dial(d.AppAddress(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithBlock())
			if conn != nil {
				defer conn.Close()
			}

			if err != nil {
				return false
			}
			in := emptypb.Empty{}
			out := rtv1.HealthCheckResponse{}
			err = conn.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallbackHealthCheck/HealthCheck", &in, &out)
			return err == nil
		}, 10*time.Second, 100*time.Millisecond)
	}
}

func (d *Daprd) GRPCConn(t *testing.T, ctx context.Context) *grpc.ClientConn {
	conn, err := grpc.DialContext(ctx, fmt.Sprintf("127.0.0.1:%d", d.GRPCPort()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return conn
}

func (d *Daprd) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	return rtv1.NewDaprClient(d.GRPCConn(t, ctx))
}

func (d *Daprd) AppID() string {
	return d.appID
}

func (d *Daprd) Namespace() string {
	return d.namespace
}

func (d *Daprd) AppPort() int {
	return d.appPort
}

func (d *Daprd) AppAddress() string {
	return "127.0.0.1:" + strconv.Itoa(d.AppPort())
}

func (d *Daprd) GRPCPort() int {
	return d.grpcPort
}

func (d *Daprd) GRPCAddress() string {
	return "127.0.0.1:" + strconv.Itoa(d.GRPCPort())
}

func (d *Daprd) HTTPPort() int {
	return d.httpPort
}

func (d *Daprd) HTTPAddress() string {
	return "127.0.0.1:" + strconv.Itoa(d.HTTPPort())
}

func (d *Daprd) InternalGRPCPort() int {
	return d.internalGRPCPort
}

func (d *Daprd) InternalGRPCAddress() string {
	return "127.0.0.1:" + strconv.Itoa(d.InternalGRPCPort())
}

func (d *Daprd) PublicPort() int {
	return d.publicPort
}

func (d *Daprd) MetricsPort() int {
	return d.metricsPort
}

func (d *Daprd) ProfilePort() int {
	return d.profilePort
}
