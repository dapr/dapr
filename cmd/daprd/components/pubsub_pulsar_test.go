//go:build unit && (allcomponents || stablecomponents)

/*
Copyright 2026 The Dapr Authors
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

package components

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	avro "github.com/iskorotkov/avro/v2"
)

func TestPulsarAvroCollectionAllocationLimits(t *testing.T) {
	var header [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(header[:], uint64(maxAvroCollectionAllocSize+1)<<1)

	tests := map[string]struct {
		schema string
		target any
		err    string
	}{
		"array": {
			schema: `{"type":"array","items":"long"}`,
			target: new([]int64),
			err:    "Config.MaxSliceAllocSize",
		},
		"map": {
			schema: `{"type":"map","values":"long"}`,
			target: new(map[string]int64),
			err:    "Config.MaxMapAllocSize",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			schema, err := avro.Parse(test.schema)
			require.NoError(t, err)
			require.ErrorContains(t, avro.Unmarshal(schema, header[:n], test.target), test.err)
		})
	}
}
