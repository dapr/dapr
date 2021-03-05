package nameresolution

import (
	"fmt"

	"github.com/dapr/components-contrib/nameresolution"
	"github.com/dapr/dapr/pkg/logger"
)

type resolver struct {
	logger logger.Logger
}

// NewResolver creates Kubernetes name resolver.
func NewResolver(logger logger.Logger) nameresolution.Resolver {
	return &resolver{logger: logger}
}

// Init initializes Kubernetes name resolver.
func (k *resolver) Init(metadata nameresolution.Metadata) error {
	return nil
}

// ResolveID resolves name to address in Kubernetes.
func (k *resolver) ResolveID(req nameresolution.ResolveRequest) (string, error) {
	// Dapr requires this formatting for Kubernetes services
	fmt.Println("%s-dapr.svc.cluster.local:%d", req.ID, req.Port)
	return fmt.Sprintf("%s-dapr.svc.cluster.local:%d", req.ID, req.Port), nil
}