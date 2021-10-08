// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package grpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServerConfig(t *testing.T) {
	vals := []interface{}{
		"app1",
		"localhost:5050",
		50001,
		"1.2.3.4",
		"default",
		"td1",
		4,
		"",
		4,
	}

	c := NewServerConfig(vals[0].(string), vals[1].(string), vals[2].(int), []string{vals[3].(string)}, vals[4].(string), vals[5].(string), vals[6].(int), vals[7].(string), vals[8].(int))
	assert.Equal(t, vals[0], c.AppID)
	assert.Equal(t, vals[1], c.HostAddress)
	assert.Equal(t, vals[2], c.Port)
	assert.Equal(t, vals[3], c.APIListenAddresses[0])
	assert.Equal(t, vals[4], c.NameSpace)
	assert.Equal(t, vals[5], c.TrustDomain)
	assert.Equal(t, vals[6], c.MaxRequestBodySize)
	assert.Equal(t, vals[8], c.ReadBufferSize)
}
