// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestSetEnvVariables(t *testing.T) {
	t.Run("Should set environment variables", func(t *testing.T) {
		variables := map[string]string{
			"ABC_ID":   "123",
			"ABC_PORT": "234",
			"ABC_HOST": "456",
		}

		err := setEnvVariables(variables)

		assert.NoError(t, err)

		for key, value := range variables {
			assert.Equal(t, value, os.Getenv(key))
		}
	})
}
