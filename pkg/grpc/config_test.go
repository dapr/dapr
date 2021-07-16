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
		"default",
		"td1",
		4,
		"1.2.3.4",
	}

	c := NewServerConfig(vals[0].(string), vals[1].(string), vals[2].(int), vals[3].(string), vals[4].(string), vals[5].(int), vals[6].(string))
	assert.Equal(t, vals[0], c.AppID)
	assert.Equal(t, vals[1], c.HostAddress)
	assert.Equal(t, vals[2], c.Port)
	assert.Equal(t, vals[3], c.NameSpace)
	assert.Equal(t, vals[4], c.TrustDomain)
	assert.Equal(t, vals[5], c.MaxRequestBodySize)
	assert.Equal(t, vals[6], c.APIListenAddress)
}
