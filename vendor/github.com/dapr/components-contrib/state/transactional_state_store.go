// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

// TransactionalStateStore is an interface for initialization and support multiple transactional requests
type TransactionalStateStore interface {
	Init(metadata Metadata) error
	Multi(reqs []TransactionalRequest) error
}
