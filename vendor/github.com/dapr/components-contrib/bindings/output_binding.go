// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

// OutputBinding is the interface for an output binding, allowing users to invoke remote systems with optional payloads
type OutputBinding interface {
	Init(metadata Metadata) error
	Write(req *WriteRequest) error
}
