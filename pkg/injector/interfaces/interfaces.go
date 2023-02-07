package interfaces

import "context"

// Injector is the interface for the Dapr runtime sidecar injection component.
type Injector interface {
	Run(ctx context.Context, onReady func())
	UpdateAllowedAuthUIDs(addAuthUIDs []string, deleteAuthUIDs []string)
}
