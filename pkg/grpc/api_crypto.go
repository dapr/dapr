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
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwk"
	"google.golang.org/grpc"

	contribCrypto "github.com/dapr/components-contrib/crypto"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/messaging"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/kit/ptr"
	encv1 "github.com/dapr/kit/schemes/enc/v1"
)

// Timeout for waiting for the first message in the stream for Encrypt/Decrypt requests.
const cryptoFirstChunkTimeout = 5 * time.Second

// EncryptAlpha1 encrypts a message using the Dapr encryption scheme and a key stored in the vault.
func (a *api) EncryptAlpha1(stream runtimev1pb.Dapr_EncryptAlpha1Server) (err error) { //nolint:nosnakecase
	// Get the first message from the caller containing the options
	reqProto := &runtimev1pb.EncryptAlpha1Request{}
	err = cryptoGetFirstChunk(stream, reqProto)
	if err != nil {
		// This is already an APIError object.
		a.Logger.Debug(err)
		return err
	}

	// Validate required options
	if reqProto.Options == nil {
		err = messages.ErrBadRequest.WithFormat("first message does not contain the required options")
		a.Logger.Debug(err)
		return err
	}
	if reqProto.Options.KeyName == "" {
		err = messages.ErrBadRequest.WithFormat("missing property 'keyName' in the options message")
		a.Logger.Debug(err)
		return err
	}
	if reqProto.Options.Algorithm == "" {
		err = messages.ErrBadRequest.WithFormat("missing property 'algorithm' in the options message")
		a.Logger.Debug(err)
		return err
	}

	// Validate the request and get the component
	component, err := a.CryptoValidateRequest(reqProto.Options.ComponentName)
	if err != nil {
		// Error is already logged
		return err
	}

	// Options
	encOpts := encv1.EncryptOptions{
		KeyName:   reqProto.Options.KeyName,
		Algorithm: encv1.KeyAlgorithm(strings.ToUpper(reqProto.Options.Algorithm)),
		WrapKeyFn: a.cryptoGetWrapKeyFn(stream.Context(), reqProto.Options.ComponentName, component),

		// The next values are optional and could be empty
		OmitKeyName:       reqProto.Options.OmitDecryptionKeyName,
		DecryptionKeyName: reqProto.Options.DecryptionKey,
	}

	// Set the cipher if present
	if reqProto.Options.Cipher != "" {
		encOpts.Cipher = ptr.Of(encv1.Cipher(strings.ToUpper(reqProto.Options.Cipher)))
	}

	// Process the request as a stream
	return a.cryptoProcessStream(stream, reqProto, encOpts)
}

// DecryptAlpha1 decrypts a message using the Dapr encryption scheme and a key stored in the vault.
func (a *api) DecryptAlpha1(stream runtimev1pb.Dapr_DecryptAlpha1Server) (err error) { //nolint:nosnakecase
	// Get the first message from the caller containing the options
	reqProto := &runtimev1pb.DecryptAlpha1Request{}
	err = cryptoGetFirstChunk(stream, reqProto)
	if err != nil {
		// This is already an APIError object.
		a.Logger.Debug(err)
		return err
	}

	// Validate required options
	if reqProto.Options == nil {
		err = messages.ErrBadRequest.WithFormat("first message does not contain the required options")
		a.Logger.Debug(err)
		return err
	}

	// Validate the request and get the component
	component, err := a.CryptoValidateRequest(reqProto.Options.ComponentName)
	if err != nil {
		// Error is already logged
		return err
	}

	// Options
	encOpts := encv1.DecryptOptions{
		UnwrapKeyFn: a.cryptoGetUnwrapKeyFn(stream.Context(), reqProto.Options.ComponentName, component),

		// The next values are optional and could be empty
		KeyName: reqProto.Options.KeyName,
	}

	// Process the request as a stream
	return a.cryptoProcessStream(stream, reqProto, encOpts)
}

// Processes the request as a stream, encrypting or decrypting data.
// For encryption, pass opts as an object of type encv1.EncryptOptions.
// For decryption, pass opts as an object of type encv1.DecryptOptions.
func (a *api) cryptoProcessStream(stream grpc.ServerStream, reqProto runtimev1pb.CryptoRequests, opts any) (err error) {
	// Create a pipe to send the data to encrypt
	inReader, inWriter := io.Pipe()

	// Process the data coming from the stream
	ctx := stream.Context()
	go func() {
		var (
			readSeq   uint64
			expectSeq uint64
			payload   *commonv1pb.StreamPayload
			readErr   error
		)

		// Process all chunks until EOF
		for {
			if ctx.Err() != nil {
				inWriter.CloseWithError(ctx.Err())
				return
			}

			// Get the payload from the chunk that was previously read
			payload = reqProto.GetPayload()
			if payload != nil {
				readSeq, readErr = messaging.ReadChunk(payload, inWriter)
				if readErr != nil {
					inWriter.CloseWithError(readErr)
					return
				}

				// Check if the sequence number is greater than the previous
				if readSeq != expectSeq {
					inWriter.CloseWithError(fmt.Errorf("invalid sequence number received: %d (expected: %d)", readSeq, expectSeq))
					return
				}

				expectSeq++
			}

			// Read the next chunk
			reqProto.Reset()
			readErr = stream.RecvMsg(reqProto)
			if errors.Is(readErr, io.EOF) {
				// Receiving an io.EOF signifies that the client has stopped sending data over the pipe, so we can stop reading
				inWriter.Close()
				break
			} else if readErr != nil {
				inWriter.CloseWithError(fmt.Errorf("error receiving message: %w", readErr))
				return
			}

			if reqProto.HasOptions() {
				inWriter.CloseWithError(errors.New("options found in non-leading message"))
				return
			}
		}
	}()

	// Start the encryption or decryption
	// Errors here are synchronous and can be returned to the user right away
	var (
		out      io.Reader
		resProto runtimev1pb.CryptoResponses
	)
	switch o := opts.(type) {
	case encv1.EncryptOptions:
		out, err = encv1.Encrypt(inReader, o)
		resProto = &runtimev1pb.EncryptAlpha1Response{}
	case encv1.DecryptOptions:
		out, err = encv1.Decrypt(inReader, o)
		resProto = &runtimev1pb.DecryptAlpha1Response{}
	default:
		// It's ok to panic here since this indicates a development-time error.
		a.Logger.Fatal("Invalid type for opts argument")
	}
	if err != nil {
		err = messages.ErrCryptoOperation.WithFormat(err)
		a.Logger.Debug(err)
		return err
	}

	// Get a buffer from the pool
	buf := encv1.BufPool.Get().(*[]byte)
	defer func() {
		encv1.BufPool.Put(buf)
	}()

	// Send the response to the client
	var (
		sendSeq uint64
		done    bool
	)
	for {
		if ctx.Err() != nil {
			err = messages.ErrCryptoOperation.WithFormat(ctx.Err())
			a.Logger.Debug(err)
			return err
		}

		// Read the next chunk of data
		n, err := out.Read(*buf)
		if err == io.EOF {
			done = true
		} else if err != nil {
			err = messages.ErrCryptoOperation.WithFormat(err)
			a.Logger.Debug(err)
			return err
		}

		// Send the message if there's any data
		if n > 0 {
			resProto.SetPayload(&commonv1pb.StreamPayload{
				Data: (*buf)[:n],
				Seq:  sendSeq,
			})
			sendSeq++

			err = stream.SendMsg(resProto)
			if err != nil {
				err = messages.ErrCryptoOperation.WithFormat(fmt.Errorf("error sending message: %w", err))
				a.Logger.Debug(err)
				return err
			}
		}

		// Stop with the last chunk
		// This will make the method return and close the stream
		if done {
			break
		}

		// Reset the object so we can re-use it
		// Use `resProto.Reset()` if more properties are added besides Payload to the proto
		resProto.SetPayload(nil)
	}

	return nil
}

type subtleWrapKeyRes struct {
	wrappedKey []byte
	tag        []byte
}

func (a *api) cryptoGetWrapKeyFn(ctx context.Context, componentName string, component contribCrypto.SubtleCrypto) encv1.WrapKeyFn {
	return func(plaintextKeyBytes []byte, algorithm, keyName string, nonce []byte) (wrappedKey []byte, tag []byte, err error) {
		plaintextKey, err := jwk.FromRaw(plaintextKeyBytes)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to import key: %w", err)
		}

		policyRunner := resiliency.NewRunner[subtleWrapKeyRes](ctx,
			a.Resiliency.ComponentOutboundPolicy(componentName, resiliency.Crypto),
		)
		start := time.Now()
		swkr, err := policyRunner(func(ctx context.Context) (r subtleWrapKeyRes, rErr error) {
			r.wrappedKey, r.tag, rErr = component.WrapKey(ctx, plaintextKey, algorithm, keyName, nonce, nil)
			return
		})
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.CryptoInvoked(ctx, componentName, diag.CryptoOp, err == nil, elapsed)

		if err != nil {
			return nil, nil, err
		}
		return swkr.wrappedKey, swkr.tag, nil
	}
}

func (a *api) cryptoGetUnwrapKeyFn(ctx context.Context, componentName string, component contribCrypto.SubtleCrypto) encv1.UnwrapKeyFn {
	return func(wrappedKey []byte, algorithm, keyName string, nonce, tag []byte) (plaintextKeyBytes []byte, err error) {
		policyRunner := resiliency.NewRunner[jwk.Key](ctx,
			a.Resiliency.ComponentOutboundPolicy(componentName, resiliency.Crypto),
		)
		start := time.Now()
		plaintextKey, err := policyRunner(func(ctx context.Context) (jwk.Key, error) {
			return component.UnwrapKey(ctx, wrappedKey, algorithm, keyName, nonce, tag, nil)
		})
		elapsed := diag.ElapsedSince(start)

		diag.DefaultComponentMonitoring.CryptoInvoked(ctx, componentName, diag.CryptoOp, err == nil, elapsed)

		if err != nil {
			return nil, err
		}

		err = plaintextKey.Raw(&plaintextKeyBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to extract key: %w", err)
		}

		return plaintextKeyBytes, nil
	}
}

func cryptoGetFirstChunk(stream grpc.ServerStream, reqProto any) error {
	// Wait for the first message from the caller containing the options
	// We put a timeout of 5 seconds on receiving the first message
	firstMsgCh := make(chan error, 1)
	go func() {
		firstMsgCh <- stream.RecvMsg(reqProto)
	}()

	firstChunkCtx, cancel := context.WithTimeout(stream.Context(), cryptoFirstChunkTimeout)
	defer cancel()

	select {
	case <-firstChunkCtx.Done():
		return messages.ErrBadRequest.WithFormat(fmt.Errorf("error waiting for first message: %w", firstChunkCtx.Err()))
	case err := <-firstMsgCh:
		if err != nil {
			return messages.ErrCryptoOperation.WithFormat(fmt.Errorf("error receiving the first message: %w", err))
		}
	}

	return nil
}
