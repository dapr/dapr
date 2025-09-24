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
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/metrics"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
)

type Daprd struct {
	exec       *exec.Exec
	ports      *ports.Ports
	httpClient *http.Client

	appID            string
	namespace        string
	appProtocol      string
	appPort          *int
	grpcPort         int
	httpPort         int
	internalGRPCPort int
	publicPort       int
	metricsPort      int
	profilePort      int

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Daprd {
	t.Helper()

	uid, err := uuid.NewRandom()
	require.NoError(t, err)

	fp := ports.Reserve(t, 6)
	opts := options{
		appID:            uid.String(),
		appProtocol:      "http",
		grpcPort:         fp.Port(t),
		httpPort:         fp.Port(t),
		internalGRPCPort: fp.Port(t),
		publicPort:       fp.Port(t),
		metricsPort:      fp.Port(t),
		profilePort:      fp.Port(t),
		logLevel:         "debug",
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
		"--app-protocol=" + opts.appProtocol,
		"--dapr-grpc-port=" + strconv.Itoa(opts.grpcPort),
		"--dapr-http-port=" + strconv.Itoa(opts.httpPort),
		"--dapr-internal-grpc-port=" + strconv.Itoa(opts.internalGRPCPort),
		"--dapr-internal-grpc-listen-address=127.0.0.1",
		"--dapr-listen-addresses=127.0.0.1",
		"--dapr-public-port=" + strconv.Itoa(opts.publicPort),
		"--dapr-public-listen-address=127.0.0.1",
		"--metrics-port=" + strconv.Itoa(opts.metricsPort),
		"--metrics-listen-address=127.0.0.1",
		"--profile-port=" + strconv.Itoa(opts.profilePort),
		"--enable-app-health-check=" + strconv.FormatBool(opts.appHealthCheck),
		"--app-health-probe-interval=" + strconv.Itoa(opts.appHealthProbeInterval),
		"--app-health-threshold=" + strconv.Itoa(opts.appHealthProbeThreshold),
		"--mode=" + opts.mode,
		"--enable-mtls=" + strconv.FormatBool(opts.enableMTLS),
		"--enable-profiling",
	}

	if opts.appPort != nil {
		args = append(args, "--app-port="+strconv.Itoa(*opts.appPort))
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
	for _, c := range opts.configs {
		args = append(args, "--config="+c)
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
	if len(opts.schedulerAddresses) > 0 {
		args = append(args, "--scheduler-host-address="+strings.Join(opts.schedulerAddresses, ","))
	}
	if opts.controlPlaneTrustDomain != nil {
		args = append(args, "--control-plane-trust-domain="+*opts.controlPlaneTrustDomain)
	}
	if opts.maxBodySize != nil {
		args = append(args, "--max-body-size="+*opts.maxBodySize)
	}
	if opts.allowedOrigins != nil {
		args = append(args, "--allowed-origins="+*opts.allowedOrigins)
	}

	ns := "default"
	if opts.namespace != nil {
		ns = *opts.namespace
		opts.execOpts = append(opts.execOpts, exec.WithEnvVars(t, "NAMESPACE", *opts.namespace))
	}

	return &Daprd{
		exec:             exec.New(t, binary.EnvValue("daprd"), args, opts.execOpts...),
		ports:            fp,
		httpClient:       client.HTTPWithTimeout(t, 30*time.Second),
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
	d.runOnce.Do(func() {
		d.ports.Free(t)
		d.exec.Run(t, ctx)
	})
}

func (d *Daprd) Cleanup(t *testing.T) {
	d.cleanupOnce.Do(func() {
		if d.httpClient != nil {
			d.httpClient.CloseIdleConnections()
		}
		d.exec.Cleanup(t)
	})
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
	}, 20*time.Second, 10*time.Millisecond)
}

func (d *Daprd) WaitUntilRunning(t *testing.T, ctx context.Context) {
	t.Helper()

	client := client.HTTP(t)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1.0/healthz", d.HTTPAddress()), nil)
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := client.Do(req)
		if assert.NoError(c, err) {
			defer resp.Body.Close()
			assert.Equal(c, http.StatusNoContent, resp.StatusCode)
		}
	}, 30*time.Second, 10*time.Millisecond)
}

func (d *Daprd) WaitUntilAppHealth(t *testing.T, ctx context.Context) {
	switch d.appProtocol {
	case "http":
		client := client.HTTP(t)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://%s/v1.0/healthz", d.HTTPAddress()), nil)
		require.NoError(t, err)
		assert.Eventually(t, func() bool {
			resp, err := client.Do(req)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return http.StatusNoContent == resp.StatusCode
		}, 20*time.Second, 10*time.Millisecond)

	case "grpc":
		assert.Eventually(t, func() bool {
			//nolint:staticcheck
			conn, err := grpc.Dial(d.AppAddress(t),
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
		}, 20*time.Second, 10*time.Millisecond)
	}
}

func (d *Daprd) GRPCConn(t *testing.T, ctx context.Context) *grpc.ClientConn {
	//nolint:staticcheck
	conn, err := grpc.DialContext(ctx, d.GRPCAddress(),
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

func (d *Daprd) ipPort(port int) string {
	return "127.0.0.1:" + strconv.Itoa(port)
}

func (d *Daprd) AppPort(t *testing.T) int {
	t.Helper()
	require.NotNil(t, d.appPort, "no app port specified")
	return *d.appPort
}

func (d *Daprd) AppAddress(t *testing.T) string {
	return d.ipPort(d.AppPort(t))
}

func (d *Daprd) GRPCPort() int {
	return d.grpcPort
}

func (d *Daprd) GRPCAddress() string {
	return d.ipPort(d.GRPCPort())
}

func (d *Daprd) HTTPPort() int {
	return d.httpPort
}

func (d *Daprd) HTTPAddress() string {
	return d.ipPort(d.HTTPPort())
}

func (d *Daprd) InternalGRPCPort() int {
	return d.internalGRPCPort
}

func (d *Daprd) InternalGRPCAddress() string {
	return d.ipPort(d.InternalGRPCPort())
}

func (d *Daprd) PublicPort() int {
	return d.publicPort
}

func (d *Daprd) MetricsPort() int {
	return d.metricsPort
}

func (d *Daprd) MetricsAddress() string {
	return d.ipPort(d.MetricsPort())
}

func (d *Daprd) ProfilePort() int {
	return d.profilePort
}

// Metrics Returns a subset of metrics scraped from the metrics endpoint
func (d *Daprd) Metrics(t assert.TestingT, ctx context.Context) *metrics.Metrics {
	return metrics.New(t, ctx, fmt.Sprintf("http://%s/metrics", d.MetricsAddress()))
}

func (d *Daprd) MetricResidentMemoryMi(t *testing.T, ctx context.Context) float64 {
	return d.Metrics(t, ctx).All()["process_resident_memory_bytes"] * 1e-6
}

func (d *Daprd) HTTPGet(t assert.TestingT, ctx context.Context, path string, expectedCode int) {
	d.httpxxx(t, ctx, http.MethodGet, path, nil, expectedCode)
}

func (d *Daprd) HTTPPost(t assert.TestingT, ctx context.Context, path string, body io.Reader, expectedCode int, headers ...string) {
	d.httpxxx(t, ctx, http.MethodPost, path, body, expectedCode, headers...)
}

func (d *Daprd) HTTPGet2xx(t assert.TestingT, ctx context.Context, path string) {
	d.httpxxx(t, ctx, http.MethodGet, path, nil, http.StatusOK)
}

func (d *Daprd) HTTPPost2xx(t assert.TestingT, ctx context.Context, path string, body io.Reader, headers ...string) {
	d.httpxxx(t, ctx, http.MethodPost, path, body, http.StatusOK, headers...)
}

func (d *Daprd) HTTPDelete2xx(t assert.TestingT, ctx context.Context, path string, body io.Reader, headers ...string) {
	d.httpxxx(t, ctx, http.MethodDelete, path, body, http.StatusOK, headers...)
}

func (d *Daprd) httpxxx(t assert.TestingT, ctx context.Context, method, path string, body io.Reader, expectedCode int, headers ...string) {
	assert.Zero(t, len(headers)%2, "headers must be key-value pairs")

	path = strings.TrimPrefix(path, "/")
	url := fmt.Sprintf("http://%s/%s", d.HTTPAddress(), path)
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	//nolint:testifylint
	assert.NoError(t, err)

	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	resp, err := d.httpClient.Do(req)
	if assert.NoError(t, err) {
		b, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.NoError(t, resp.Body.Close())

		assert.GreaterOrEqual(t, resp.StatusCode, expectedCode, strconv.Itoa(expectedCode)+"status code expected: "+string(b))
		assert.Less(t, resp.StatusCode, expectedCode+100, strconv.Itoa(expectedCode)+"status code expected: "+string(b))
	}
}

func (d *Daprd) GetMetaRegisteredComponents(t assert.TestingT, ctx context.Context) []*rtv1.RegisteredComponents {
	return d.meta(t, ctx).RegisteredComponents
}

func (d *Daprd) GetMetaSubscriptions(t assert.TestingT, ctx context.Context) []MetadataResponsePubsubSubscription {
	return d.meta(t, ctx).Subscriptions
}

func (d *Daprd) GetMetaSubscriptionsWithType(t assert.TestingT, ctx context.Context, subType string) []MetadataResponsePubsubSubscription {
	subs := d.GetMetaSubscriptions(t, ctx)
	var filteredSubs []MetadataResponsePubsubSubscription
	for _, sub := range subs {
		if sub.Type == subType {
			filteredSubs = append(filteredSubs, sub)
		}
	}
	return filteredSubs
}

func (d *Daprd) GetMetaHTTPEndpoints(t assert.TestingT, ctx context.Context) []*rtv1.MetadataHTTPEndpoint {
	return d.meta(t, ctx).HTTPEndpoints
}

func (d *Daprd) GetMetaScheduler(t assert.TestingT, ctx context.Context) *rtv1.MetadataScheduler {
	return d.meta(t, ctx).Scheduler
}

func (d *Daprd) GetMetaActorRuntime(t assert.TestingT, ctx context.Context) *MetadataActorRuntime {
	return d.meta(t, ctx).ActorRuntime
}

func (d *Daprd) GetMetadata(t assert.TestingT, ctx context.Context) *Metadata {
	return d.meta(t, ctx)
}

// Metadata is a subset of metadataResponse defined in pkg/api/http/metadata.go:160
type Metadata struct {
	RegisteredComponents []*rtv1.RegisteredComponents         `json:"components,omitempty"`
	Subscriptions        []MetadataResponsePubsubSubscription `json:"subscriptions,omitempty"`
	HTTPEndpoints        []*rtv1.MetadataHTTPEndpoint         `json:"httpEndpoints,omitempty"`
	Scheduler            *rtv1.MetadataScheduler              `json:"scheduler,omitempty"`
	ActorRuntime         *MetadataActorRuntime                `json:"actorRuntime,omitempty"`
	Workflows            *MetadataWorkflows                   `json:"workflows"`
}

// MetadataResponsePubsubSubscription copied from pkg/api/http/metadata.go:172 to be able to use in integration tests until we move to Proto format
type MetadataResponsePubsubSubscription struct {
	PubsubName      string                                   `json:"pubsubname"`
	Topic           string                                   `json:"topic"`
	Metadata        map[string]string                        `json:"metadata,omitempty"`
	Rules           []MetadataResponsePubsubSubscriptionRule `json:"rules,omitempty"`
	DeadLetterTopic string                                   `json:"deadLetterTopic"`
	Type            string                                   `json:"type"`
}

type MetadataResponsePubsubSubscriptionRule struct {
	Match string `json:"match,omitempty"`
	Path  string `json:"path,omitempty"`
}

type MetadataActorRuntime struct {
	RuntimeStatus string                             `json:"runtimeStatus"`
	HostReady     bool                               `json:"hostReady"`
	Placement     string                             `json:"placement"`
	ActiveActors  []*MetadataActorRuntimeActiveActor `json:"activeActors"`
}

type MetadataWorkflows struct {
	ConnectedWorkers int `json:"connectedWorkers,omitempty"`
}

type MetadataActorRuntimeActiveActor struct {
	Type  string `json:"type"`
	Count int    `json:"count"`
}

func (d *Daprd) meta(t assert.TestingT, ctx context.Context) *Metadata {
	url := fmt.Sprintf("http://%s/v1.0/metadata", d.HTTPAddress())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	//nolint:testifylint
	if !assert.NoError(t, err) {
		return nil
	}

	var meta Metadata
	resp, err := d.httpClient.Do(req)
	if assert.NoError(t, err) {
		defer resp.Body.Close()
		assert.NoError(t, json.NewDecoder(resp.Body).Decode(&meta))
	}

	return &meta
}

func (d *Daprd) ActorInvokeURL(actorType, actorID, method string) string {
	return fmt.Sprintf("http://%s/v1.0/actors/%s/%s/method/%s", d.HTTPAddress(), actorType, actorID, method)
}

func (d *Daprd) ActorReminderURL(actorType, actorID, method string) string {
	return fmt.Sprintf("http://%s/v1.0/actors/%s/%s/reminders/%s", d.HTTPAddress(), actorType, actorID, method)
}

func (d *Daprd) Kill(t *testing.T) {
	d.exec.Kill(t)
}
