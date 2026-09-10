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

package images

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtension(t *testing.T) {
	t.Run("empty list returns no extension", func(t *testing.T) {
		_, ok, err := Extension(nil)
		require.NoError(t, err)
		assert.False(t, ok)

		_, ok, err = Extension([]ContainerImage{})
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("value is an OCTET STRING wrapping the JSON bytes", func(t *testing.T) {
		imgs := []ContainerImage{
			{Role: RoleDaprd, ContainerName: "daprd", Image: "ghcr.io/dapr/daprd:1.16.0", Digest: "sha256:abc123"},
			{Role: RoleApp, ContainerName: "app", Image: "docker.io/library/myapp:v2"},
		}

		ext, ok, err := Extension(imgs)
		require.NoError(t, err)
		require.True(t, ok)
		assert.True(t, ext.Id.Equal(OIDContainerImages))
		assert.False(t, ext.Critical)

		var jsonBytes []byte
		rest, err := asn1.Unmarshal(ext.Value, &jsonBytes)
		require.NoError(t, err)
		assert.Empty(t, rest)
		assert.JSONEq(t, `[
			{"role":"daprd","containerName":"daprd","image":"ghcr.io/dapr/daprd:1.16.0","digest":"sha256:abc123"},
			{"role":"app","containerName":"app","image":"docker.io/library/myapp:v2"}
		]`, string(jsonBytes))
	})
}

func TestFromExtensions(t *testing.T) {
	imgs := []ContainerImage{
		{Role: RoleDaprd, ContainerName: "daprd", Image: "ghcr.io/dapr/daprd:1.16.0", Digest: "sha256:abc123"},
		{Role: RoleApp, ContainerName: "app", Image: "docker.io/library/myapp:v2"},
	}

	t.Run("round trip", func(t *testing.T) {
		ext, ok, err := Extension(imgs)
		require.NoError(t, err)
		require.True(t, ok)

		got, ok, err := FromExtensions([]pkix.Extension{
			{Id: asn1.ObjectIdentifier{2, 5, 29, 17}},
			ext,
		})
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, imgs, got)
	})

	t.Run("not present", func(t *testing.T) {
		got, ok, err := FromExtensions([]pkix.Extension{
			{Id: asn1.ObjectIdentifier{2, 5, 29, 17}},
		})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, got)
	})

	t.Run("malformed value", func(t *testing.T) {
		_, _, err := FromExtensions([]pkix.Extension{
			{Id: OIDContainerImages, Value: []byte("not-der")},
		})
		require.Error(t, err)
	})
}
