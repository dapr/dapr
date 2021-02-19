// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package client

type ActorClient interface {
	InvokeMethod(actorType, actorID, method string, contentType string, data []byte) ([]byte, error)
	SaveStateTransactionally(actorType, actorID string, data []byte) error
	GetState(actorType, actorID, name string) ([]byte, error)
	WaitUntilDaprIsReady() error
	Close()
}
