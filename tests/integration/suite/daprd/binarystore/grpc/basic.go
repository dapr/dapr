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

package grpc

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
}

const componentYAML = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: binarystore.fake
  version: v1
`

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.daprd = daprd.New(t, daprd.WithResourceFiles(componentYAML))
	return []framework.Option{
		framework.WithProcesses(b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	client := b.daprd.GRPCClient(t, ctx)

	t.Run("set (overwrite) then get round-trips", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "hello.bin",
					Overwrite:     new(true),
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("hello world"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)

		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "hello.bin",
		})
		require.NoError(t, err)

		var got []byte
		for {
			msg, err := getStream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			got = append(got, msg.GetPayload().GetData()...)
		}
		assert.Equal(t, []byte("hello world"), got)
	})

	t.Run("set validates file name", func(t *testing.T) {
		tests := []struct {
			name     string
			fileName string
			code     codes.Code
		}{
			{name: "valid", fileName: "valid-name.bin", code: codes.OK},
			{name: "nested path", fileName: "a/b.bin", code: codes.InvalidArgument},
			{name: "parent path", fileName: "../x.bin", code: codes.InvalidArgument},
			{name: "leading slash", fileName: "/x.bin", code: codes.InvalidArgument},
			{name: "backslash", fileName: `a\b.bin`, code: codes.InvalidArgument},
			{name: "dot", fileName: ".", code: codes.InvalidArgument},
			{name: "parent directory", fileName: "..", code: codes.InvalidArgument},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				stream, err := client.SetBinaryFileAlpha1(ctx)
				require.NoError(t, err)
				require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
					SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
						InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
							ComponentName: "mystore",
							FileName:      test.fileName,
							Overwrite:     new(true),
						},
					},
				}))
				require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
					SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
						Payload: &commonv1pb.StreamPayload{
							Data: []byte("payload"),
							Seq:  0,
						},
					},
				}))
				_, err = stream.CloseAndRecv()
				assert.Equal(t, test.code, status.Code(err))
			})
		}
	})

	t.Run("total upload larger than default body limit succeeds with smaller chunks", func(t *testing.T) {
		payload := bytes.Repeat([]byte{0xAB}, 5<<20)
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "large.bin",
					Overwrite:     new(true),
				},
			},
		}))
		const chunkSize = 1 << 20
		var seq uint64
		for offset := 0; offset < len(payload); offset += chunkSize {
			end := min(offset+chunkSize, len(payload))
			require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
				SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
					Payload: &commonv1pb.StreamPayload{
						Data: payload[offset:end],
						Seq:  seq,
					},
				},
			}))
			seq++
		}
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)

		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "large.bin",
		})
		require.NoError(t, err)

		var got []byte
		for {
			msg, err := getStream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			got = append(got, msg.GetPayload().GetData()...)
		}
		assert.Equal(t, payload, got)
	})

	t.Run("chunk larger than default body limit fails", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "oversized-chunk.bin",
					Overwrite:     new(true),
				},
			},
		}))

		sendErr := stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{
					Data: bytes.Repeat([]byte{0xAB}, 5<<20),
					Seq:  0,
				},
			},
		})
		_, closeErr := stream.CloseAndRecv()
		if closeErr != nil {
			err = closeErr
		} else {
			err = sendErr
		}
		require.Error(t, err)
		assert.Equal(t, codes.ResourceExhausted, status.Code(err))
	})

	t.Run("out-of-order chunk sequence fails", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "out-of-order.bin",
					Overwrite:     new(true),
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{
					Data: []byte("second"),
					Seq:  1,
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{
					Data: []byte("first"),
					Seq:  0,
				},
			},
		}))

		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "invalid sequence number received: 1 (expected: 0)")
	})

	t.Run("set without overwrite conflicts", func(t *testing.T) {
		// "hello.bin" already exists from the previous subtest.
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "hello.bin",
					Overwrite:     new(false),
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("second"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("get missing file returns NotFound", func(t *testing.T) {
		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "missing.bin",
		})
		require.NoError(t, err)
		_, err = getStream.Recv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("delete then get returns NotFound", func(t *testing.T) {
		stream, err := client.SetBinaryFileAlpha1(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_InitialRequest{
				InitialRequest: &rtv1.SetBinaryFileRequestInitialAlpha1{
					ComponentName: "mystore",
					FileName:      "temp.bin",
					Overwrite:     new(true),
				},
			},
		}))
		require.NoError(t, stream.Send(&rtv1.SetBinaryFileRequest{
			SetBinaryFileRequestType: &rtv1.SetBinaryFileRequest_Payload{
				Payload: &commonv1pb.StreamPayload{Data: []byte("temp"), Seq: 0},
			},
		}))
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)

		_, err = client.DeleteBinaryFileAlpha1(ctx, &rtv1.DeleteBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "temp.bin",
		})
		require.NoError(t, err)

		getStream, err := client.GetBinaryFileAlpha1(ctx, &rtv1.GetBinaryFileRequest{
			ComponentName: "mystore",
			FileName:      "temp.bin",
		})
		require.NoError(t, err)
		_, err = getStream.Recv()
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("component not found returns InvalidArgument", func(t *testing.T) {
		_, err := client.DeleteBinaryFileAlpha1(ctx, &rtv1.DeleteBinaryFileRequest{
			ComponentName: "does-not-exist",
			FileName:      "x.bin",
		})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}
