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

package http

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/pkg/api/universal"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestCryptoEndpoints(t *testing.T) {
	fakeServer := newFakeHTTPServer()

	compStore := compstore.New()
	const cryptoComponentName = "myvault"
	compStore.AddCryptoProvider(cryptoComponentName, &daprt.FakeSubtleCrypto{})
	testAPI := &api{
		universal: universal.New(universal.Options{
			Logger:     log,
			CompStore:  compStore,
			Resiliency: resiliency.New(nil),
		}),
	}

	fakeServer.StartServer(testAPI.constructCryptoEndpoints(), nil)
	defer fakeServer.Shutdown()

	const testMessage = "Respiri piano per non far rumore, ti addormenti di sera, ti risvegli con il sole, sei chiara come un'alba, sei fresca come l'aria."

	var encMessage []byte
	t.Run("Encrypt successfully - 200", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, cryptoComponentName)
		headers := []string{
			cryptoHeaderKeyName, "aes-passthrough",
			cryptoHeaderKeyWrapAlgorithm, "AES",
		}
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, []byte(testMessage), nil, headers...)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/octet-stream", resp.ContentType)
		assert.NotEmpty(t, resp.RawBody)
		assert.True(t, bytes.HasPrefix(resp.RawBody, []byte("dapr.io/enc/v1\n")))
		encMessage = resp.RawBody
	})

	t.Run("Decrypt successfully - 200", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/crypto/%s/decrypt", apiVersionV1alpha1, cryptoComponentName)
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, encMessage, nil)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/octet-stream", resp.ContentType)
		assert.NotEmpty(t, resp.RawBody)
		assert.Equal(t, testMessage, string(resp.RawBody))
	})

	t.Run("No crypto providers configured - 500", func(t *testing.T) {
		compStore.DeleteCryptoProvider(cryptoComponentName)
		defer func() {
			compStore.AddCryptoProvider(cryptoComponentName, &daprt.FakeSubtleCrypto{})
		}()

		resps := map[string]fakeHTTPResponse{
			"encrypt": fakeServer.DoRequest(http.MethodPut, fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, cryptoComponentName), []byte(testMessage), map[string]string{
				cryptoHeaderKeyName:          "aes-passthrough",
				cryptoHeaderKeyWrapAlgorithm: "AES",
			}),
			"decrypt": fakeServer.DoRequest(http.MethodPut, fmt.Sprintf("%s/crypto/%s/decrypt", apiVersionV1alpha1, cryptoComponentName), encMessage, nil),
		}

		for name, resp := range resps {
			t.Run(name, func(t *testing.T) {
				require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
				require.NotEmpty(t, resp.ErrorBody)
				assert.Equal(t, "ERR_CRYPTO_PROVIDERS_NOT_CONFIGURED", resp.ErrorBody["errorCode"])
			})
		}
	})

	t.Run("Component not found - 400", func(t *testing.T) {
		resps := map[string]fakeHTTPResponse{
			"encrypt": fakeServer.DoRequest(http.MethodPut, fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, "not-found"), []byte(testMessage), map[string]string{
				cryptoHeaderKeyName:          "aes-passthrough",
				cryptoHeaderKeyWrapAlgorithm: "AES",
			}),
			"decrypt": fakeServer.DoRequest(http.MethodPut, fmt.Sprintf("%s/crypto/%s/decrypt", apiVersionV1alpha1, "not-found"), encMessage, nil),
		}

		for name, resp := range resps {
			t.Run(name, func(t *testing.T) {
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
				require.NotEmpty(t, resp.ErrorBody)
				assert.Equal(t, "ERR_CRYPTO_PROVIDER_NOT_FOUND", resp.ErrorBody["errorCode"])
				assert.Equal(t, "crypto provider not-found not found", resp.ErrorBody["message"])
			})
		}
	})

	t.Run("Encrypt - missing keyName - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, cryptoComponentName)
		headers := []string{
			// cryptoHeaderKeyName,   "aes-passthrough",
			cryptoHeaderKeyWrapAlgorithm, "AES",
		}
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, []byte(testMessage), nil, headers...)

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotEmpty(t, resp.ErrorBody)
		assert.Equal(t, "ERR_BAD_REQUEST", resp.ErrorBody["errorCode"])
		assert.Contains(t, resp.ErrorBody["message"], "missing header 'dapr-key-name'")
	})

	t.Run("Encrypt - missing algorithm - 400", func(t *testing.T) {
		apiPath := fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, cryptoComponentName)
		headers := []string{
			cryptoHeaderKeyName, "aes-passthrough",
			// cryptoHeaderKeyWrapAlgorithm, "AES",
		}
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, []byte(testMessage), nil, headers...)

		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
		require.NotEmpty(t, resp.ErrorBody)
		assert.Equal(t, "ERR_BAD_REQUEST", resp.ErrorBody["errorCode"])
		assert.Contains(t, resp.ErrorBody["message"], "missing header 'dapr-key-wrap-algorithm'")
	})

	t.Run("Encryption fails - 500", func(t *testing.T) {
		// Simulates an error in key wrapping
		apiPath := fmt.Sprintf("%s/crypto/%s/encrypt", apiVersionV1alpha1, cryptoComponentName)
		headers := []string{
			cryptoHeaderKeyName, "error",
			cryptoHeaderKeyWrapAlgorithm, "AES",
		}
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, []byte(testMessage), nil, headers...)

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.NotEmpty(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CRYPTO", resp.ErrorBody["errorCode"])
		assert.Equal(t, "failed to perform operation: failed to wrap the file key: simulated error", resp.ErrorBody["message"])
	})

	t.Run("Decryption fails - 500", func(t *testing.T) {
		// The body is invalid
		apiPath := fmt.Sprintf("%s/crypto/%s/decrypt", apiVersionV1alpha1, cryptoComponentName)
		resp := fakeServer.DoRequest(http.MethodPut, apiPath, []byte("not-valid"), nil)

		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.NotEmpty(t, resp.ErrorBody)
		assert.Equal(t, "ERR_CRYPTO", resp.ErrorBody["errorCode"])
		assert.Contains(t, resp.ErrorBody["message"], "failed to perform operation: invalid header")
	})
}
