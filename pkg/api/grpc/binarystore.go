/*
Copyright 2025 The Dapr Authors
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
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

// Timeout for waiting for the first message in the stream for SetBinaryFileAlpha1.
const binaryStoreFirstChunkTimeout = 5 * time.Second

// SetBinaryFileAlpha1 stores a binary file uploaded as a stream of chunks.
func (a *api) SetBinaryFileAlpha1(stream runtimev1pb.Dapr_SetBinaryFileAlpha1Server) (err error) { //nolint:nosnakecase
	// Get the first message from the caller containing the options
	reqProto := &runtimev1pb.SetBinaryFileRequest{}
	if err = a.binaryStoreGetFirstChunk(stream, reqProto); err != nil {
		a.logger.Debug(err)
		return err
	}

	// Validate required options
	if reqProto.GetOptions() == nil {
		err = messages.ErrBadRequest.WithFormat("first message does not contain the required options")
		a.logger.Debug(err)
		return err
	}
	if reqProto.GetOptions().GetComponentName() == "" {
		err = messages.ErrBadRequest.WithFormat("missing property 'componentName' in the options message")
		a.logger.Debug(err)
		return err
	}
	if reqProto.GetOptions().GetFileName() == "" {
		err = messages.ErrBinaryStoreNameMissing
		a.logger.Debug(err)
		return err
	}

	// Pipe the streamed payload to the universal layer which forwards it to
	// the component as an io.Reader, avoiding buffering the whole file.
	inReader, inWriter := io.Pipe()

	ctx := stream.Context()

	writerDone := make(chan error, 1)
	a.wg.Go(func() {
		writerDone <- a.binaryStoreReadStream(ctx, stream, reqProto, inWriter)
	})

	setErr := a.Universal.SetBinaryFileAlpha1(ctx,
		reqProto.GetOptions().GetComponentName(),
		reqProto.GetOptions().GetFileName(),
		reqProto.GetOptions().GetOverwrite(),
		inReader,
	)
	// Closing the reader unblocks the writer goroutine if it is still waiting
	// on a full pipe (e.g. the component rejected the upload early).
	_ = inReader.CloseWithError(setErr)

	wErr := <-writerDone
	if setErr != nil {
		// setErr is already an APIError.
		a.logger.Debug(setErr)
		return setErr
	}
	if wErr != nil {
		err = messages.ErrBinaryStoreSet.WithFormat(
			reqProto.GetOptions().GetFileName(),
			reqProto.GetOptions().GetComponentName(),
			wErr.Error(),
		)
		a.logger.Debug(err)
		return err
	}

	return nil
}

// GetBinaryFileAlpha1 retrieves a binary file and streams it back as chunks.
func (a *api) GetBinaryFileAlpha1(req *runtimev1pb.GetBinaryFileRequest, stream runtimev1pb.Dapr_GetBinaryFileAlpha1Server) (err error) { //nolint:nosnakecase
	componentName := req.GetComponentName()
	fileName := req.GetFileName()

	body, err := a.Universal.GetBinaryFileAlpha1(stream.Context(), componentName, fileName)
	if err != nil {
		a.logger.Debug(err)
		return err
	}
	defer func() {
		// Ensure the streamed reader is always closed to release the underlying
		// HTTP connection / resources, even on read or send errors.
		_ = body.Close()
	}()

	buf := make([]byte, 32*1024)
	var (
		seq  uint64
		n    int
		read error
	)
	for {
		if stream.Context().Err() != nil {
			err = messages.ErrBinaryStoreGet.WithFormat(fileName, componentName, stream.Context().Err().Error())
			a.logger.Debug(err)
			return err
		}

		n, read = body.Read(buf)
		if n > 0 {
			sendErr := stream.Send(&runtimev1pb.GetBinaryFileResponse{
				Payload: &commonv1pb.StreamPayload{
					Data: append([]byte(nil), buf[:n]...),
					Seq:  seq,
				},
			})
			if sendErr != nil {
				err = messages.ErrBinaryStoreGet.WithFormat(fileName, componentName, fmt.Errorf("error sending message: %w", sendErr).Error())
				a.logger.Debug(err)
				return err
			}
			seq++
		}

		if read == io.EOF {
			return nil
		}
		if read != nil {
			err = messages.ErrBinaryStoreGet.WithFormat(fileName, componentName, read.Error())
			a.logger.Debug(err)
			return err
		}
	}
}

// DeleteBinaryFileAlpha1 deletes a binary file.
func (a *api) DeleteBinaryFileAlpha1(ctx context.Context, req *runtimev1pb.DeleteBinaryFileRequest) (*runtimev1pb.DeleteBinaryFileResponse, error) { //nolint:nosnakecase
	if err := a.Universal.DeleteBinaryFileAlpha1(ctx, req.GetComponentName(), req.GetFileName()); err != nil {
		a.logger.Debug(err)
		return nil, err
	}
	return &runtimev1pb.DeleteBinaryFileResponse{}, nil
}

// binaryStoreGetFirstChunk waits for the first message in the stream (with a
// timeout) and decodes it into reqProto.
func (a *api) binaryStoreGetFirstChunk(stream runtimev1pb.Dapr_SetBinaryFileAlpha1Server, reqProto *runtimev1pb.SetBinaryFileRequest) error {
	firstChunkCtx, cancel := context.WithTimeout(stream.Context(), binaryStoreFirstChunkTimeout)
	defer cancel()

	firstMsgCh := make(chan error, 1)
	a.wg.Go(func() {
		select {
		case firstMsgCh <- stream.RecvMsg(reqProto):
		case <-firstChunkCtx.Done():
		}
	})

	select {
	case <-firstChunkCtx.Done():
		return messages.ErrBadRequest.WithFormat(fmt.Errorf("error waiting for first message: %w", firstChunkCtx.Err()))
	case err := <-firstMsgCh:
		if err != nil {
			return messages.ErrBinaryStoreSet.WithFormat("", "", fmt.Errorf("error receiving the first message: %w", err).Error())
		}
	}

	return nil
}

// binaryStoreReadStream drains the remaining chunks from the client stream into
// the provided writer, enforcing sequence numbers and rejecting options in
// non-leading messages.
func (a *api) binaryStoreReadStream(ctx context.Context, stream runtimev1pb.Dapr_SetBinaryFileAlpha1Server, reqProto *runtimev1pb.SetBinaryFileRequest, inWriter *io.PipeWriter) error {
	var (
		readSeq   uint64
		expectSeq uint64
	)

	for {
		if ctx.Err() != nil {
			return inWriter.CloseWithError(ctx.Err())
		}

		// Process the payload carried by the message currently held in reqProto
		// (this may be set from the first message).
		if payload := reqProto.GetPayload(); payload != nil {
			rSeq, err := messaging.ReadChunk(payload, inWriter)
			if err != nil {
				return inWriter.CloseWithError(err)
			}
			readSeq = rSeq
			if readSeq != expectSeq {
				return inWriter.CloseWithError(fmt.Errorf("invalid sequence number received: %d (expected: %d)", readSeq, expectSeq))
			}
			expectSeq++
		}

		// Read the next chunk
		reqProto.Reset()
		readErr := stream.RecvMsg(reqProto)
		if errors.Is(readErr, io.EOF) {
			return inWriter.Close()
		} else if readErr != nil {
			return inWriter.CloseWithError(fmt.Errorf("error receiving message: %w", readErr))
		}

		if reqProto.GetOptions() != nil {
			return inWriter.CloseWithError(errors.New("options found in non-leading message"))
		}
	}
}
