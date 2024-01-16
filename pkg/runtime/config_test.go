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

package runtime

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/ptr"
)

func Test_toInternal(t *testing.T) {
	cfg := defaultTestConfig()

	var nilDuration *time.Duration

	intc, err := cfg.toInternal()
	require.NoError(t, err)

	assert.Equal(t, "app1", intc.id)
	assert.Equal(t, "localhost:5050", intc.actorsService)
	assert.Equal(t, "localhost:5051", intc.kubernetes.ControlPlaneAddress)
	assert.Equal(t, "*", intc.allowedOrigins)
	_ = assert.Len(t, intc.standalone.ResourcesPath, 1) &&
		assert.Equal(t, "components", intc.standalone.ResourcesPath[0])
	assert.Equal(t, "http", string(intc.appConnectionConfig.Protocol))
	assert.Equal(t, "kubernetes", string(intc.mode))
	assert.Equal(t, 3500, intc.httpPort)
	assert.Equal(t, 50002, intc.internalGRPCPort)
	assert.Equal(t, 50001, intc.apiGRPCPort)
	assert.Equal(t, ptr.Of(3501), intc.publicPort)
	assert.Equal(t, "1.2.3.4", intc.apiListenAddresses[0])
	assert.Equal(t, 8080, intc.appConnectionConfig.Port)
	assert.Equal(t, 7070, intc.profilePort)
	assert.True(t, intc.enableProfiling)
	assert.Equal(t, 1, intc.appConnectionConfig.MaxConcurrency)
	assert.True(t, intc.mTLSEnabled)
	assert.Equal(t, "localhost:5052", intc.sentryServiceAddress)
	assert.Equal(t, 4, intc.maxRequestBodySize)
	assert.Equal(t, "", intc.unixDomainSocket)
	assert.Equal(t, 4, intc.readBufferSize)
	assert.Equal(t, time.Second, intc.gracefulShutdownDuration)
	assert.Equal(t, nilDuration, intc.blockShutdownDuration)
	assert.Equal(t, ptr.Of(true), intc.enableAPILogging)
	assert.True(t, intc.disableBuiltinK8sSecretStore)
	assert.Equal(t, "1.1.1.1", intc.appConnectionConfig.ChannelAddress)
}

func TestStandaloneWasmStrictSandbox(t *testing.T) {
	global, err := config.LoadStandaloneConfiguration("../config/testdata/wasm_strict_sandbox.yaml")

	require.NoError(t, err)
	assert.True(t, global.Spec.WasmSpec.StrictSandbox)
}

func defaultTestConfig() Config {
	return Config{
		AppID:                        "app1",
		ActorsService:                "localhost:5050",
		ControlPlaneAddress:          "localhost:5051",
		AllowedOrigins:               "*",
		ResourcesPath:                []string{"components"},
		AppProtocol:                  "http",
		Mode:                         "kubernetes",
		DaprHTTPPort:                 "3500",
		DaprInternalGRPCPort:         "50002",
		DaprAPIGRPCPort:              "50001",
		DaprAPIListenAddresses:       "1.2.3.4",
		DaprPublicPort:               "3501",
		ApplicationPort:              "8080",
		ProfilePort:                  "7070",
		EnableProfiling:              true,
		AppMaxConcurrency:            1,
		EnableMTLS:                   true,
		SentryAddress:                "localhost:5052",
		DaprHTTPMaxRequestSize:       4,
		UnixDomainSocket:             "",
		DaprHTTPReadBufferSize:       4,
		DaprGracefulShutdownSeconds:  1,
		EnableAPILogging:             ptr.Of(true),
		DisableBuiltinK8sSecretStore: true,
		AppChannelAddress:            "1.1.1.1",
		Registry:                     registry.NewOptions(),
		Metrics:                      &metrics.Options{MetricsEnabled: false},
	}
}
