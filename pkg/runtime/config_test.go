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
)

func TestNewConfig(t *testing.T) {
	publicPort := DefaultDaprPublicPort
	c := NewRuntimeConfig(NewRuntimeConfigOpts{
		ID:                           "app1",
		PlacementAddresses:           []string{"localhost:5050"},
		ControlPlaneAddress:          "localhost:5051",
		AllowedOrigins:               "*",
		ResourcesPath:                []string{"components"},
		AppProtocol:                  "http",
		Mode:                         "kubernetes",
		HTTPPort:                     3500,
		InternalGRPCPort:             50002,
		APIGRPCPort:                  50001,
		APIListenAddresses:           []string{"1.2.3.4"},
		PublicPort:                   &publicPort,
		AppPort:                      8080,
		ProfilePort:                  7070,
		EnableProfiling:              true,
		MaxConcurrency:               1,
		MTLSEnabled:                  true,
		SentryAddress:                "localhost:5052",
		MaxRequestBodySize:           4,
		UnixDomainSocket:             "",
		ReadBufferSize:               4,
		GracefulShutdownDuration:     time.Second,
		EnableAPILogging:             true,
		DisableBuiltinK8sSecretStore: true,
		AppChannelAddress:            "1.1.1.1",
		EnableAppHealthCheck:         true,
		AppHealthCheckPath:           "/healthz",
		AppHealthProbeInterval:       1 * time.Second,
		AppHealthProbeTimeout:        2 * time.Second,
		AppHealthThreshold:           3,
	})

	assert.Equal(t, "app1", c.ID)
	assert.Equal(t, "localhost:5050", c.PlacementAddresses[0])
	assert.Equal(t, "localhost:5051", c.Kubernetes.ControlPlaneAddress)
	assert.Equal(t, "*", c.AllowedOrigins)
	_ = assert.Len(t, c.Standalone.ResourcesPath, 1) &&
		assert.Equal(t, "components", c.Standalone.ResourcesPath[0])
	assert.Equal(t, "kubernetes", string(c.Mode))
	assert.Equal(t, 3500, c.HTTPPort)
	assert.Equal(t, 50002, c.InternalGRPCPort)
	assert.Equal(t, 50001, c.APIGRPCPort)
	assert.Equal(t, &publicPort, c.PublicPort)
	assert.Equal(t, "1.2.3.4", c.APIListenAddresses[0])
	assert.Equal(t, 8080, c.ApplicationPort)
	assert.Equal(t, 7070, c.ProfilePort)
	assert.Equal(t, true, c.EnableProfiling)
	assert.Equal(t, true, c.mtlsEnabled)
	assert.Equal(t, "localhost:5052", c.SentryServiceAddress)
	assert.Equal(t, 4, c.MaxRequestBodySize)
	assert.Equal(t, "", c.UnixDomainSocket)
	assert.Equal(t, 4, c.ReadBufferSize)
	assert.Equal(t, time.Second, c.GracefulShutdownDuration)
	assert.Equal(t, true, c.EnableAPILogging)
	assert.Equal(t, true, c.DisableBuiltinK8sSecretStore)
	assert.Equal(t, "1.1.1.1", c.AppConnectionConfig.ChannelAddress)
	assert.Equal(t, 8080, c.AppConnectionConfig.Port)
	assert.Equal(t, "http", string(c.AppConnectionConfig.Protocol))
	assert.Equal(t, 1, c.AppConnectionConfig.MaxConcurrency)
	assert.Equal(t, "/healthz", c.AppConnectionConfig.HealthCheckHTTPPath)
	assert.Equal(t, 1*time.Second, c.AppConnectionConfig.HealthCheck.ProbeInterval)
	assert.Equal(t, 2*time.Second, c.AppConnectionConfig.HealthCheck.ProbeTimeout)
	assert.Equal(t, 3, int(c.AppConnectionConfig.HealthCheck.Threshold))
}
