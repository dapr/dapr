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

package options

import (
	"testing"
	"time"

	"github.com/dapr/kit/ptr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlacementAddr(t *testing.T) {
	testCases := []struct {
		addr string
		out  []string
	}{
		{
			addr: "localhost:1020",
			out:  []string{"localhost:1020"},
		},
		{
			addr: "placement1:50005,placement2:50005,placement3:50005",
			out:  []string{"placement1:50005", "placement2:50005", "placement3:50005"},
		},
		{
			addr: "placement1:50005, placement2:50005, placement3:50005",
			out:  []string{"placement1:50005", "placement2:50005", "placement3:50005"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.addr, func(t *testing.T) {
			assert.EqualValues(t, tc.out, parsePlacementAddr(tc.addr))
		})
	}
}

func Test_parseValues(t *testing.T) {
	fs := flagSet{
		appID:                        "app1",
		placementServiceHostAddr:     "localhost:5050",
		controlPlaneAddress:          "localhost:5051",
		allowedOrigins:               "*",
		resourcesPath:                []string{"components"},
		appProtocol:                  "http",
		mode:                         "kubernetes",
		daprHTTPPort:                 "3500",
		daprInternalGRPCPort:         "50002",
		daprAPIGRPCPort:              "50001",
		daprAPIListenAddresses:       "1.2.3.4",
		daprPublicPort:               "3501",
		appPort:                      "8080",
		profilePort:                  "7070",
		enableProfiling:              true,
		appMaxConcurrency:            1,
		enableMTLS:                   true,
		sentryAddress:                "localhost:5052",
		daprHTTPMaxRequestSize:       4,
		unixDomainSocket:             "",
		daprHTTPReadBufferSize:       4,
		daprGracefulShutdownSeconds:  1,
		enableAPILogging:             true,
		disableBuiltinK8sSecretStore: true,
		appChannelAddress:            "1.1.1.1",
	}

	opts, err := fs.parseValues()
	require.NoError(t, err)

	assert.Equal(t, "app1", opts.AppID)
	assert.Equal(t, "localhost:5050", opts.PlacementAddresses[0])
	assert.Equal(t, "localhost:5051", opts.ControlPlaneAddress)
	assert.Equal(t, "*", opts.AllowedOrigins)
	_ = assert.Len(t, opts.ResourcesPaths, 1) &&
		assert.Equal(t, "components", opts.ResourcesPaths[0])
	assert.Equal(t, "http", string(opts.AppProtocol))
	assert.Equal(t, "kubernetes", string(opts.Mode))
	assert.Equal(t, 3500, opts.DaprHTTPPort)
	assert.Equal(t, 50002, opts.DaprInternalGRPCPort)
	assert.Equal(t, 50001, opts.DaprAPIGRPCPort)
	assert.Equal(t, ptr.Of(3501), opts.DaprPublicPort)
	assert.Equal(t, "1.2.3.4", opts.DaprAPIListenAddressList[0])
	assert.Equal(t, 8080, opts.AppPort)
	assert.Equal(t, 7070, opts.ProfilePort)
	assert.Equal(t, true, opts.EnableProfiling)
	assert.Equal(t, 1, opts.AppMaxConcurrency)
	assert.Equal(t, true, opts.EnableMTLS)
	assert.Equal(t, "localhost:5052", opts.SentryAddress)
	assert.Equal(t, 4, opts.DaprHTTPMaxRequestSize)
	assert.Equal(t, "", opts.UnixDomainSocket)
	assert.Equal(t, 4, opts.DaprHTTPReadBufferSize)
	assert.Equal(t, time.Second, opts.GracefulShutdownDuration)
	assert.Equal(t, ptr.Of(true), opts.EnableAPILogging)
	assert.Equal(t, true, opts.DisableBuiltinK8sSecretStore)
	assert.Equal(t, "1.1.1.1", opts.AppChannelAddress)
}
