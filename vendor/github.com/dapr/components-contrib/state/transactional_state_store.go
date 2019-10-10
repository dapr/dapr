// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

type TransactionalStateStore interface {
	Init(metadata Metadata) error
	Multi(reqs []TransactionalRequest) error
}
