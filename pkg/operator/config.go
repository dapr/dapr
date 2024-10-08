package operator

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/security"
)

// Config returns an operator config options.
type Config struct {
	MTLSEnabled             bool
	ControlPlaneTrustDomain string
	SentryAddress           string
}

// LoadConfiguration loads the Kubernetes configuration and returns an Operator Config.
func LoadConfiguration(ctx context.Context, name string, restConfig *rest.Config) (*Config, error) {
	scheme, err := buildScheme(Options{})
	if err != nil {
		return nil, err
	}

	client, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("could not get Kubernetes API client: %w", err)
	}

	namespace, err := security.CurrentNamespaceOrError()
	if err != nil {
		return nil, err
	}

	var conf v1alpha1.Configuration
	key := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}
	if err := client.Get(ctx, key, &conf); err != nil {
		return nil, err
	}
	return &Config{
		MTLSEnabled:             conf.Spec.MTLSSpec.GetEnabled(),
		ControlPlaneTrustDomain: conf.Spec.MTLSSpec.ControlPlaneTrustDomain,
		SentryAddress:           conf.Spec.MTLSSpec.SentryAddress,
	}, nil
}
