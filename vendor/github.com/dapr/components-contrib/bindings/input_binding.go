// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

// InputBinding is the interface to define a binding that triggers on incoming events
type InputBinding interface {
	// Init passes connection and properties metadata to the binding implementation
	Init(metadata Metadata) error
	// Read is a blocking method that triggers the callback function whenever an event arrives
	Read(handler func(*ReadResponse) error) error
}
