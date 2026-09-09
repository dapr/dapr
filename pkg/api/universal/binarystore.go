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

package universal

import (
	"context"
	"errors"
	"io"

	"github.com/dapr/components-contrib/binarystore"
	"github.com/dapr/dapr/pkg/messages"
	"github.com/dapr/dapr/pkg/resiliency"
)

// getBinaryStore looks up an initialised binary store component by name,
// returning an APIError suitable for direct return to the caller if missing.
func (a *Universal) getBinaryStore(componentName string) (binarystore.BinaryStore, error) {
	component, ok := a.compStore.GetBinaryStore(componentName)
	if !ok {
		err := messages.ErrBinaryStoreNotFound.WithFormat(componentName)
		a.logger.Debug(err)
		return nil, err
	}
	return component, nil
}

// SetBinaryFileAlpha1 stores binary content streamed from r into the named
// file in the given component. When overwrite is false the operation fails
// with ErrBinaryStoreFileExists if the file already exists.
//
// The caller must not close r until this method returns.
func (a *Universal) SetBinaryFileAlpha1(ctx context.Context, componentName, fileName string, overwrite bool, r io.Reader) error {
	if fileName == "" {
		err := messages.ErrBinaryStoreNameMissing
		a.logger.Debug(err)
		return err
	}

	component, err := a.getBinaryStore(componentName)
	if err != nil {
		return err
	}

	req := &binarystore.SetRequest{
		FileName:  fileName,
		Data:      r,
		Overwrite: overwrite,
	}

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(componentName, resiliency.Binarystore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, component.Set(ctx, req)
	})
	if err != nil {
		return mapBinaryStoreError(err, componentName, fileName, messages.ErrBinaryStoreSet)
	}
	return nil
}

// GetBinaryFileAlpha1 retrieves the named file from the given component and
// returns a streaming reader. The caller is responsible for closing the
// returned reader when reading is complete.
func (a *Universal) GetBinaryFileAlpha1(ctx context.Context, componentName, fileName string) (io.ReadCloser, error) {
	if fileName == "" {
		err := messages.ErrBinaryStoreNameMissing
		a.logger.Debug(err)
		return nil, err
	}

	component, err := a.getBinaryStore(componentName)
	if err != nil {
		return nil, err
	}

	req := &binarystore.GetRequest{FileName: fileName}

	policyRunner := resiliency.NewRunner[*binarystore.GetResponse](ctx,
		a.resiliency.ComponentOutboundPolicy(componentName, resiliency.Binarystore),
	)
	resp, err := policyRunner(func(ctx context.Context) (*binarystore.GetResponse, error) {
		return component.Get(ctx, req)
	})
	if err != nil {
		return nil, mapBinaryStoreError(err, componentName, fileName, messages.ErrBinaryStoreGet)
	}
	if resp == nil || resp.Data == nil {
		// Defensive: a nil reader should never be returned for an existing file,
		// but treat it as not-found rather than returning nil to the caller.
		err := messages.ErrBinaryStoreFileNotFound.WithFormat(fileName, componentName)
		a.logger.Debug(err)
		return nil, err
	}
	return resp.Data, nil
}

// DeleteBinaryFileAlpha1 removes the named file from the given component.
func (a *Universal) DeleteBinaryFileAlpha1(ctx context.Context, componentName, fileName string) error {
	if fileName == "" {
		err := messages.ErrBinaryStoreNameMissing
		a.logger.Debug(err)
		return err
	}

	component, err := a.getBinaryStore(componentName)
	if err != nil {
		return err
	}

	req := &binarystore.DeleteRequest{FileName: fileName}

	policyRunner := resiliency.NewRunner[any](ctx,
		a.resiliency.ComponentOutboundPolicy(componentName, resiliency.Binarystore),
	)
	_, err = policyRunner(func(ctx context.Context) (any, error) {
		return nil, component.Delete(ctx, req)
	})
	if err != nil {
		return mapBinaryStoreError(err, componentName, fileName, messages.ErrBinaryStoreDelete)
	}
	return nil
}

// mapBinaryStoreError translates contrib sentinel errors into APIError values
// with appropriate HTTP/gRPC status codes, wrapping everything else as a
// generic operation failure using the provided fallback APIError.
func mapBinaryStoreError(err error, componentName, fileName string, fallback messages.APIError) error {
	switch {
	case errors.Is(err, binarystore.ErrFileNotFound):
		return messages.ErrBinaryStoreFileNotFound.WithFormat(fileName, componentName)
	case errors.Is(err, binarystore.ErrFileAlreadyExists):
		return messages.ErrBinaryStoreFileExists.WithFormat(fileName, componentName)
	case errors.Is(err, binarystore.ErrMissingFileName):
		return messages.ErrBinaryStoreNameMissing
	default:
		return fallback.WithFormat(fileName, componentName, err.Error())
	}
}
