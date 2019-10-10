// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

type OutputBinding interface {
	Init(metadata Metadata) error
	Write(req *WriteRequest) error
}
