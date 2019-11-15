// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicediscovery

type Resolver interface {
	ResolveID(req ResolveRequest) (string, error)
}
