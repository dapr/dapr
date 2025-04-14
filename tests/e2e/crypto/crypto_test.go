//go:build e2e
// +build e2e

/*
Copyright 2024 The Dapr Authors
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

package crypto_e2e

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
)

var tr *runner.TestRunner

const (
	// Number of get calls before starting tests.
	numHealthChecks = 10

	// Name of the test file
	testFileName = "gunnar-ridderstrom-LhuSa4p41uI-unsplash.jpg"
)

var (
	testComponents []string
	httpClient     *http.Client

	// TODO: Remove when the SubtleCrypto feature is finalized
	subtleCryptoEnabled bool
)

func TestMain(m *testing.M) {
	utils.SetupLogs("cryptoapp")
	utils.InitHTTPClient(true)

	log.Printf("AZURE_KEY_VAULT_NAME=%s", os.Getenv("AZURE_KEY_VAULT_NAME"))

	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        "cryptoapp",
			DaprEnabled:    true,
			ImageName:      "e2e-crypto",
			Replicas:       1,
			IngressEnabled: true,
			// This is used by the azurekeyvault component envRef. It will be empty if not running on Azure
			DaprEnv: "AZURE_KEY_VAULT_NAME=" + os.Getenv("AZURE_KEY_VAULT_NAME"),
		},
	}

	// This test uses the Azure Key Vault component only if running on Azure
	testComponents = []string{"jwks"}
	if e := os.Getenv("DAPR_TEST_CRYPTO"); e != "" {
		testComponents = []string{e}
	}

	// We need the test HTTP client
	httpClient = utils.GetHTTPClient()

	log.Print("Creating TestRunner")
	tr = runner.NewTestRunner("metadatatest", testApps, nil, nil)
	log.Print("Starting TestRunner")
	os.Exit(tr.Start(m))
}

func TestWaitReady(t *testing.T) {
	appExternalURL := tr.Platform.AcquireAppExternalURL("cryptoapp")
	require.NotEmpty(t, appExternalURL, "appExternalURL must not be empty")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(appExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Check if the SubtleCrypto features are enabled
	// TODO: Remove when the SubtleCrypto feature is finalized
	u := fmt.Sprintf("http://%s/getSubtleCryptoEnabled", appExternalURL)
	res, err := httpClient.Get(utils.SanitizeHTTPURL(u))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusOK, res.StatusCode)

	resData, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	switch string(resData) {
	case "0":
		subtleCryptoEnabled = false
	case "1":
		subtleCryptoEnabled = true
	default:
		t.Fatalf("Invalid response from /getSubtleCryptoEnabled: %s", string(resData))
	}
	t.Logf("Subtle Crypto APIs enabled: %v", subtleCryptoEnabled)
}

func TestCrypto(t *testing.T) {
	appExternalURL := tr.Platform.AcquireAppExternalURL("cryptoapp")
	require.NotEmpty(t, appExternalURL, "appExternalURL must not be empty")

	// Read the test file and keep it in memory while also computing its SHA-256 checksum
	testFileData, testFileHash := readTestFile(t)

	testFn := func(protocol, component string) func(t *testing.T) {
		return func(t *testing.T) {
			var encFile []byte

			t.Run("encrypt", func(t *testing.T) {
				// Encrypt the file and store the result in-memory
				u := fmt.Sprintf("http://%s/test/%s/%s/encrypt?key=rsakey&alg=RSA", appExternalURL, protocol, component)
				res, err := httpClient.Post(utils.SanitizeHTTPURL(u), "", bytes.NewReader(testFileData))
				require.NoError(t, err)
				defer res.Body.Close()
				require.Equal(t, http.StatusOK, res.StatusCode)

				encFile, err = io.ReadAll(res.Body)
				require.NoError(t, err)

				// Encrypted file starts with the right "magic numbers" and is larger than the plaintext
				assert.True(t, bytes.HasPrefix(encFile, []byte("dapr.io/enc/v1\n")))
				assert.Greater(t, len(encFile), len(testFileData))
			})

			t.Run("decrypt", func(t *testing.T) {
				// Decrypt the encrypted file
				u := fmt.Sprintf("http://%s/test/%s/%s/decrypt", appExternalURL, protocol, component)
				res, err := httpClient.Post(utils.SanitizeHTTPURL(u), "", bytes.NewReader(encFile))
				require.NoError(t, err)
				defer res.Body.Close()
				require.Equal(t, http.StatusOK, res.StatusCode)

				// Read the response
				h := sha256.New()
				_, err = io.Copy(h, res.Body)
				require.NoError(t, err)

				// Compare the checksum
				require.Equal(t, testFileHash, h.Sum(nil), "checksum of decrypted file does not match")
			})
		}
	}

	// Encrypt a file using the high-level APIs, then decrypt it
	for _, protocol := range []string{"grpc", "http"} {
		t.Run(protocol, func(t *testing.T) {
			for _, component := range testComponents {
				t.Run(component, testFn(protocol, component))
			}
		})
	}
}

func TestSubtleCrypto(t *testing.T) {
	if !subtleCryptoEnabled {
		t.Skip("Skipping Subtle Crypto APIs test because they are not enabled in the binary")
	}

	appExternalURL := tr.Platform.AcquireAppExternalURL("cryptoapp")
	require.NotEmpty(t, appExternalURL, "appExternalURL must not be empty")

	testFn := func(protocol, component string) func(t *testing.T) {
		return func(t *testing.T) {
			const message = "La nebbia agli irti colli Piovigginando sale"
			messageDigest := sha256.Sum256([]byte(message))
			nonce, _ := hex.DecodeString("000102030405060708090A0B")
			aad, _ := hex.DecodeString("AABBCC")

			var (
				tempData1 []byte
				tempData2 []byte
			)

			testOpFn := func(method string, getProtos func() (proto.Message, proto.Message), assertFn func(t *testing.T, res proto.Message)) func(t *testing.T) {
				return func(t *testing.T) {
					reqData, resData := getProtos()

					reqBody, err := protojson.Marshal(reqData)
					require.NoError(t, err)

					u := fmt.Sprintf("http://%s/test/subtle/%s/%s", appExternalURL, protocol, method)
					res, err := httpClient.Post(utils.SanitizeHTTPURL(u), "", bytes.NewReader(reqBody))
					require.NoError(t, err)
					defer res.Body.Close()
					require.Equal(t, http.StatusOK, res.StatusCode)

					resBody, err := io.ReadAll(res.Body)
					require.NoError(t, err)

					err = protojson.Unmarshal(resBody, resData)
					require.NoError(t, err)

					assertFn(t, resData)
				}
			}

			type testDef struct {
				testName  string
				method    string
				getProtos func() (proto.Message, proto.Message)
				assertFn  func(t *testing.T, res proto.Message)
			}

			tests := []testDef{
				{
					"get key as PEM",
					"getkey",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleGetKeyRequest{
								ComponentName: component,
								Name:          "rsakey",
								// PEM is the default format
								// Format: runtimev1pb.SubtleGetKeyRequest_PEM,
							},
							&runtimev1pb.SubtleGetKeyResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleGetKeyResponse)
						require.NotEmpty(t, res.Name)
						assert.Contains(t, res.Name, "rsakey")
						require.NotEmpty(t, res.PublicKey)

						block, _ := pem.Decode([]byte(res.PublicKey))
						require.NotEmpty(t, block)
						assert.Equal(t, "PUBLIC KEY", block.Type)
					},
				},
				{
					"get key as JSON",
					"getkey",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleGetKeyRequest{
								ComponentName: component,
								Name:          "rsakey",
								Format:        runtimev1pb.SubtleGetKeyRequest_JSON,
							},
							&runtimev1pb.SubtleGetKeyResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleGetKeyResponse)
						require.NotEmpty(t, res.Name)
						assert.Contains(t, res.Name, "rsakey")
						require.NotEmpty(t, res.PublicKey)

						key := map[string]any{}
						err := json.Unmarshal([]byte(res.PublicKey), &key)
						require.NoError(t, err)
						require.Equal(t, "RSA", key["kty"])
						// Public parameters are included
						require.NotEmpty(t, key["n"])
						require.NotEmpty(t, key["e"])
						// Private parameters are not included
						require.Empty(t, key["d"])
					},
				},
				{
					"encrypt",
					"encrypt",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleEncryptRequest{
								ComponentName: component,
								Plaintext:     []byte(message),
								Algorithm:     "RSA-OAEP",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleEncryptResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleEncryptResponse)
						require.NotEmpty(t, res.Ciphertext)

						tempData1 = res.Ciphertext
					},
				},
				{
					"decrypt",
					"decrypt",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleDecryptRequest{
								ComponentName: component,
								Ciphertext:    tempData1,
								Algorithm:     "RSA-OAEP",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleDecryptResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleDecryptResponse)
						require.NotEmpty(t, res.Plaintext)
						require.Equal(t, message, string(res.Plaintext))
					},
				},
				{
					"wrap key",
					"wrapkey",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleWrapKeyRequest{
								ComponentName: component,
								PlaintextKey:  []byte(message),
								Algorithm:     "RSA-OAEP",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleWrapKeyResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleWrapKeyResponse)
						require.NotEmpty(t, res.WrappedKey)

						tempData1 = res.WrappedKey
					},
				},
				{
					"unwrap key",
					"unwrapkey",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleUnwrapKeyRequest{
								ComponentName: component,
								WrappedKey:    tempData1,
								Algorithm:     "RSA-OAEP",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleUnwrapKeyResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleUnwrapKeyResponse)
						require.NotEmpty(t, res.PlaintextKey)
						require.Equal(t, message, string(res.PlaintextKey))
					},
				},
				{
					"sign",
					"sign",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleSignRequest{
								ComponentName: component,
								Digest:        messageDigest[:],
								Algorithm:     "PS256",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleSignResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleSignResponse)
						require.NotEmpty(t, res.Signature)

						tempData1 = res.Signature
					},
				},
				{
					"verify",
					"verify",
					func() (proto.Message, proto.Message) {
						return &runtimev1pb.SubtleVerifyRequest{
								ComponentName: component,
								Digest:        messageDigest[:],
								Signature:     tempData1,
								Algorithm:     "PS256",
								KeyName:       "rsakey",
							},
							&runtimev1pb.SubtleVerifyResponse{}
					},
					func(t *testing.T, resAny proto.Message) {
						t.Helper()

						res := resAny.(*runtimev1pb.SubtleVerifyResponse)
						require.True(t, res.Valid)
					},
				},
			}

			// The following tests require symmetric keys and do not work on Azure Key Vault (because we're not using a Managed HSM)
			if component == "jwks" {
				tests = append(tests,
					testDef{
						"encrypt with symmetric key",
						"encrypt",
						func() (proto.Message, proto.Message) {
							return &runtimev1pb.SubtleEncryptRequest{
									ComponentName:  component,
									Plaintext:      []byte(message),
									Algorithm:      "C20P",
									KeyName:        "symmetrickey",
									Nonce:          nonce,
									AssociatedData: aad,
								},
								&runtimev1pb.SubtleEncryptResponse{}
						},
						func(t *testing.T, resAny proto.Message) {
							t.Helper()

							res := resAny.(*runtimev1pb.SubtleEncryptResponse)
							require.NotEmpty(t, res.Ciphertext)
							require.NotEmpty(t, res.Tag)

							tempData1 = res.Ciphertext
							tempData2 = res.Tag
						},
					},
					testDef{
						"decrypt with symmetric key",
						"decrypt",
						func() (proto.Message, proto.Message) {
							return &runtimev1pb.SubtleDecryptRequest{
									ComponentName:  component,
									Ciphertext:     tempData1,
									Tag:            tempData2,
									Algorithm:      "C20P",
									KeyName:        "symmetrickey",
									Nonce:          nonce,
									AssociatedData: aad,
								},
								&runtimev1pb.SubtleDecryptResponse{}
						},
						func(t *testing.T, resAny proto.Message) {
							t.Helper()

							res := resAny.(*runtimev1pb.SubtleDecryptResponse)
							require.NotEmpty(t, res.Plaintext)
							require.Equal(t, message, string(res.Plaintext))
						},
					},
					testDef{
						"wrap key with symmetric key",
						"wrapkey",
						func() (proto.Message, proto.Message) {
							return &runtimev1pb.SubtleWrapKeyRequest{
									ComponentName: component,
									PlaintextKey:  []byte(message[0:16]),
									Algorithm:     "A256KW",
									KeyName:       "symmetrickey",
								},
								&runtimev1pb.SubtleWrapKeyResponse{}
						},
						func(t *testing.T, resAny proto.Message) {
							t.Helper()

							res := resAny.(*runtimev1pb.SubtleWrapKeyResponse)
							require.NotEmpty(t, res.WrappedKey)

							tempData1 = res.WrappedKey
						},
					},
					testDef{
						"unwrap key with symmetric key",
						"unwrapkey",
						func() (proto.Message, proto.Message) {
							return &runtimev1pb.SubtleUnwrapKeyRequest{
									ComponentName: component,
									WrappedKey:    tempData1,
									Algorithm:     "A256KW",
									KeyName:       "symmetrickey",
								},
								&runtimev1pb.SubtleUnwrapKeyResponse{}
						},
						func(t *testing.T, resAny proto.Message) {
							t.Helper()

							res := resAny.(*runtimev1pb.SubtleUnwrapKeyResponse)
							require.NotEmpty(t, res.PlaintextKey)
							require.Equal(t, message[0:16], string(res.PlaintextKey))
						},
					},
				)
			}

			for _, tc := range tests {
				t.Run(tc.testName, testOpFn(tc.method, tc.getProtos, tc.assertFn))
			}
		}
	}

	// Run tests for HTTP and gRPC
	for _, protocol := range []string{"grpc", "http"} {
		t.Run(protocol, func(t *testing.T) {
			for _, component := range testComponents {
				t.Run(component, testFn(protocol, component))
			}
		})
	}
}

// Read the test file and keep it in memory while also computing its SHA-256 checksum
func readTestFile(t *testing.T) (testFileData []byte, testFileHash []byte) {
	t.Helper()

	f, err := os.Open(testFileName)
	require.NoError(t, err, "failed to open test file")
	defer f.Close()

	testFileData, err = io.ReadAll(f)
	require.NoError(t, err, "failed to read test file")
	require.NotEmpty(t, testFileData)

	h := sha256.New()
	_, err = h.Write(testFileData)
	require.NoError(t, err, "failed to hash test file")
	testFileHash = h.Sum(nil)

	return
}
