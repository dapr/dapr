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

package internals

import (
	"encoding/base64"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
)

func TestMetadataToInternalMetadata(t *testing.T) {
	keyBinValue := []byte{101, 200}
	testMD := metadata.Pairs(
		"key", "key value",
		"key-bin", string(keyBinValue),
	)
	testMD.Append("multikey", "ciao", "mamma")
	internalMD := MetadataToInternalMetadata(testMD)

	require.Equal(t, 1, len(internalMD["key"].GetValues()))
	assert.Equal(t, "key value", internalMD["key"].GetValues()[0])

	require.Equal(t, 1, len(internalMD["key-bin"].GetValues()))
	assert.Equal(t, base64.StdEncoding.EncodeToString(keyBinValue), internalMD["key-bin"].GetValues()[0], "binary metadata must be saved")

	require.Equal(t, 2, len(internalMD["multikey"].GetValues()))
	assert.Equal(t, []string{"ciao", "mamma"}, internalMD["multikey"].GetValues())
}

func TestHTTPHeadersToInternalMetadata(t *testing.T) {
	header := http.Header{}
	header.Add("foo", "test")
	header.Add("bar", "test2")
	header.Add("bar", "test3")

	imd := HTTPHeadersToInternalMetadata(header)

	require.NotEmpty(t, imd)
	require.NotEmpty(t, imd["Foo"])
	require.NotEmpty(t, imd["Foo"].Values)
	assert.Equal(t, []string{"test"}, imd["Foo"].Values)
	require.NotEmpty(t, imd["Bar"])
	require.NotEmpty(t, imd["Bar"].Values)
	assert.Equal(t, []string{"test2", "test3"}, imd["Bar"].Values)
}
