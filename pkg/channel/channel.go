// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package channel

// AppChannel is an abstraction over communications with user code
type AppChannel interface {
	GetBaseAddress() string
	InvokeMethod(req *InvokeRequest) (*InvokeResponse, error)
}
