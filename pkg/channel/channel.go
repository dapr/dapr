// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package channel

import "context"

// AppChannel is an abstraction over communications with user code
type AppChannel interface {
	GetBaseAddress() string
	InvokeMethod(ctx context.Context, req *InvokeRequest) (*InvokeResponse, error)
}
