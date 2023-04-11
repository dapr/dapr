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

package grpc

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	"github.com/dapr/dapr/pkg/grpc/universalapi"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestCryptoAlpha1(t *testing.T) {
	fakeAPI := &api{
		UniversalAPI: &universalapi.UniversalAPI{
			Logger:     apiServerLogger,
			Resiliency: resiliency.New(nil),
			CryptoProviders: map[string]contribCrypto.SubtleCrypto{
				"myvault": &daprt.FakeSubtleCrypto{},
			},
		},
	}

	// Run test server
	port, _ := freeport.GetFreePort()
	server := startDaprAPIServer(port, fakeAPI, "")
	defer server.Stop()

	// Create gRPC test client
	clientConn := createTestClient(port)
	defer clientConn.Close()

	client := runtimev1pb.NewDaprClient(clientConn)

	t.Run("data and options in single chunk", func(t *testing.T) {
		var enc []byte
		t.Run("encrypt", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: []byte("hello world"),
					},
				},
			}
			enc, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(enc, []byte("dapr.io/enc/v1")))
		})

		t.Run("decrypt", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: enc,
					},
				},
			}
			dec, err := cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.NoError(t, err)
			require.Equal(t, "hello world", string(dec))
		})
	})

	t.Run("one data chunk", func(t *testing.T) {
		var enc []byte
		t.Run("encrypt", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
				},
				&runtimev1pb.EncryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: []byte("hello world"),
					},
				},
			}
			enc, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(enc, []byte("dapr.io/enc/v1")))
		})

		t.Run("decrypt", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: enc,
					},
				},
			}
			dec, err := cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.NoError(t, err)
			require.Equal(t, "hello world", string(dec))
		})
	})

	t.Run("multiple data chunks", func(t *testing.T) {
		var enc []byte
		t.Run("encrypt", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
				},
				&runtimev1pb.EncryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: []byte("soft kitty, warm kitty, little ball of fur, "),
					},
				},
				&runtimev1pb.EncryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  1,
						Data: []byte("happy kitty, sleepy kitty, purr purr purr"), //nolint:dupword
					},
				},
			}
			enc, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.NoError(t, err)
			require.True(t, bytes.HasPrefix(enc, []byte("dapr.io/enc/v1")))
		})

		t.Run("decrypt - whole header in first chunk", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: enc[0:180],
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  1,
						Data: enc[180:],
					},
				},
			}
			dec, err := cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.NoError(t, err)
			require.Equal(t, "soft kitty, warm kitty, little ball of fur, happy kitty, sleepy kitty, purr purr purr", string(dec)) //nolint:dupword
		})

		t.Run("decrypt - header split in multiple chunks", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: enc[0:50],
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  1,
						Data: enc[50:180],
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Payload: &commonv1pb.StreamPayload{
						Seq:  2,
						Data: enc[180:],
					},
				},
			}
			dec, err := cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.NoError(t, err)
			require.Equal(t, "soft kitty, warm kitty, little ball of fur, happy kitty, sleepy kitty, purr purr purr", string(dec)) //nolint:dupword
		})
	})

	// This is used to encrypt a large document so we can use it for testing
	var largeEnc []byte
	t.Run("encrypt large document", func(t *testing.T) {
		largeData := make([]byte, 100<<10) // 100KB
		_, err := io.ReadFull(rand.Reader, largeData)
		require.NoError(t, err)

		stream, err := client.EncryptAlpha1(context.Background())
		require.NoError(t, err)
		send := []runtimev1pb.CryptoRequests{
			&runtimev1pb.EncryptAlpha1Request{
				Options: &runtimev1pb.EncryptAlpha1RequestOptions{
					ComponentName: "myvault",
					KeyName:       "aes-passthrough",
					Algorithm:     "AES",
				},
				Payload: &commonv1pb.StreamPayload{
					Seq:  0,
					Data: largeData,
				},
			},
		}
		largeEnc, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
		require.NoError(t, err)
		require.Greater(t, len(largeEnc), len(largeData))
		require.True(t, bytes.HasPrefix(largeEnc, []byte("dapr.io/enc/v1")))
	})

	t.Run("decrypt without header", func(t *testing.T) {
		stream, err := client.DecryptAlpha1(context.Background())
		require.NoError(t, err)
		send := []runtimev1pb.CryptoRequests{
			&runtimev1pb.DecryptAlpha1Request{
				Options: &runtimev1pb.DecryptAlpha1RequestOptions{
					ComponentName: "myvault",
				},
				Payload: &commonv1pb.StreamPayload{
					Seq:  0,
					Data: []byte("foo"),
				},
			},
		}
		_, err = cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
		require.Error(t, err)
		require.ErrorContains(t, err, "invalid header")
	})

	t.Run("invalid sequence number", func(t *testing.T) {
		t.Run("encrypt", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  1, // Skipped 0
						Data: []byte("hello world"),
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "invalid sequence number received: 1 (expected: 0)")
		})

		t.Run("decrypt", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  1, // Skipped 0
						Data: largeEnc,
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "invalid sequence number received: 1 (expected: 0)")
		})
	})

	t.Run("options in non-leading message", func(t *testing.T) {
		t.Run("encrypt", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: []byte("hello world"),
					},
				},
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						Algorithm:     "AES",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "options found in non-leading message")
		})

		t.Run("decrypt", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
					Payload: &commonv1pb.StreamPayload{
						Seq:  0,
						Data: largeEnc,
					},
				},
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						ComponentName: "myvault",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "options found in non-leading message")
		})
	})

	t.Run("encrypt without required options", func(t *testing.T) {
		t.Run("missing options", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: nil,
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "first message does not contain the required options")
		})

		t.Run("missing component name", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						// ComponentName: "myvault",
						KeyName:   "aes-passthrough",
						Algorithm: "AES",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "missing component name")
		})

		t.Run("missing key name", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						// KeyName:       "aes-passthrough",
						Algorithm: "AES",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "missing property 'key' in the options message")
		})

		t.Run("missing algorithm", func(t *testing.T) {
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.EncryptAlpha1Request{
					Options: &runtimev1pb.EncryptAlpha1RequestOptions{
						ComponentName: "myvault",
						KeyName:       "aes-passthrough",
						// Algorithm:     "AES",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "missing property 'algorithm' in the options message")
		})
	})

	t.Run("decrypt without required options", func(t *testing.T) {
		t.Run("missing options", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: nil,
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.DecryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "first message does not contain the required options")
		})

		t.Run("missing component name", func(t *testing.T) {
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)
			send := []runtimev1pb.CryptoRequests{
				&runtimev1pb.DecryptAlpha1Request{
					Options: &runtimev1pb.DecryptAlpha1RequestOptions{
						// ComponentName: "myvault",
					},
				},
			}
			_, err = cryptoSendRequest(stream, send, &runtimev1pb.EncryptAlpha1Response{})
			require.Error(t, err)
			require.ErrorContains(t, err, "missing component name")
		})
	})

	t.Run("time out while waiting for first chunk", func(t *testing.T) {
		t.Run("encrypt", func(t *testing.T) {
			start := time.Now()
			stream, err := client.EncryptAlpha1(context.Background())
			require.NoError(t, err)

			_, err = stream.Recv()
			require.Error(t, err)
			require.ErrorContains(t, err, "error waiting for first message")
			require.GreaterOrEqual(t, time.Since(start), cryptoFirstChunkTimeout)
		})

		t.Run("decrypt", func(t *testing.T) {
			start := time.Now()
			stream, err := client.DecryptAlpha1(context.Background())
			require.NoError(t, err)

			_, err = stream.Recv()
			require.Error(t, err)
			require.ErrorContains(t, err, "error waiting for first message")
			require.GreaterOrEqual(t, time.Since(start), cryptoFirstChunkTimeout)
		})
	})
}

func cryptoSendRequest(stream grpc.ClientStream, send []runtimev1pb.CryptoRequests, recv runtimev1pb.CryptoResponses) ([]byte, error) {
	var err error

	// Send messages in a background goroutine
	sendErrCh := make(chan error)
	go func() {
		for _, msg := range send {
			err = stream.SendMsg(msg)
			if err != nil {
				sendErrCh <- fmt.Errorf("failed to send message: %w", err)
				return
			}
		}

		err = stream.CloseSend()
		if err != nil {
			sendErrCh <- fmt.Errorf("failed to close send stream: %w", err)
			return
		}

		sendErrCh <- nil
	}()

	// Receive responses
	var (
		done bool
		seq  uint64
	)
	res := &bytes.Buffer{}
	for !done {
		recv.Reset()
		err = stream.RecvMsg(recv)
		if errors.Is(err, io.EOF) {
			err = nil
			done = true
		}
		if err != nil {
			return nil, fmt.Errorf("failed to receive message: %w", err)
		}

		payload := recv.GetPayload()
		if payload != nil {
			if payload.Seq != seq {
				return nil, fmt.Errorf("expected sequence %d but got %d", seq, payload.Seq)
			}
			seq++

			if len(payload.Data) > 0 {
				_, err = res.Write(payload.Data)
				if err != nil {
					return nil, fmt.Errorf("failed to write data into buffer: %w", err)
				}
			}
		}
	}

	// Makes sure that the send side is done too
	err = <-sendErrCh
	if err != nil {
		return nil, err
	}

	return res.Bytes(), nil
}
