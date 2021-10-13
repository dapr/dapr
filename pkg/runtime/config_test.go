// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewConfig(t *testing.T) {
	publicPort := DefaultDaprPublicPort
	c := NewRuntimeConfig("app1", []string{"localhost:5050"}, "localhost:5051", "*", "config", "components", "http", "kubernetes",
		3500, 50002, 50001, []string{"1.2.3.4"}, &publicPort, 8080, 7070, true, 1, true, "localhost:5052", true, 4, "", 4, true)

	assert.Equal(t, "app1", c.ID)
	assert.Equal(t, "localhost:5050", c.PlacementAddresses[0])
	assert.Equal(t, "localhost:5051", c.Kubernetes.ControlPlaneAddress)
	assert.Equal(t, "*", c.AllowedOrigins)
	assert.Equal(t, "config", c.GlobalConfig)
	assert.Equal(t, "components", c.Standalone.ComponentsPath)
	assert.Equal(t, "http", string(c.ApplicationProtocol))
	assert.Equal(t, "kubernetes", string(c.Mode))
	assert.Equal(t, 3500, c.HTTPPort)
	assert.Equal(t, 50002, c.InternalGRPCPort)
	assert.Equal(t, 50001, c.APIGRPCPort)
	assert.Equal(t, &publicPort, c.PublicPort)
	assert.Equal(t, "1.2.3.4", c.APIListenAddresses[0])
	assert.Equal(t, 8080, c.ApplicationPort)
	assert.Equal(t, 7070, c.ProfilePort)
	assert.Equal(t, true, c.EnableProfiling)
	assert.Equal(t, 1, c.MaxConcurrency)
	assert.Equal(t, true, c.mtlsEnabled)
	assert.Equal(t, "localhost:5052", c.SentryServiceAddress)
	assert.Equal(t, true, c.AppSSL)
	assert.Equal(t, 4, c.MaxRequestBodySize)
	assert.Equal(t, "", c.UnixDomainSocket)
	assert.Equal(t, 4, c.ReadBufferSize)
	assert.Equal(t, true, c.StreamRequestBody)
}
