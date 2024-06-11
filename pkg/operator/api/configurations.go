/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"k8s.io/apimachinery/pkg/types"

	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	"github.com/dapr/dapr/pkg/operator/api/authz"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
)

const defaultConfig = "daprsystem"

var defaultConfigObjKey = client.ObjectKey{Name: defaultConfig, Namespace: os.Getenv("NAMESPACE")}

func (a *apiServer) getDaprSystemConfiguration(ctx context.Context) (*configurationapi.Configuration, error) {
	cfg := &configurationapi.Configuration{}
	// as we're using controller-runtime client, we will be using the cache/watch of it, making this call not expensive and up to date
	err := a.Client.Get(ctx, defaultConfigObjKey, cfg)
	if err != nil {
		return nil, fmt.Errorf("trying to get configuration %s: %w", defaultConfigObjKey.String(), err)
	}
	return cfg, nil
}

// sortMetricsSpec Apply .metrics if set. If not, retain .metric. following
// https://github.com/dapr/dapr/blob/50757fed0b8545f55714885c97a1543607118c4c/pkg/config/configuration.go#L665 but for the k8s type
func sortMetricsSpec(c *configurationapi.Configuration) {
	if c.Spec.MetricsSpec == nil {
		return
	}

	if c.Spec.MetricsSpec.Enabled != nil {
		if c.Spec.MetricSpec == nil {
			c.Spec.MetricSpec = &configurationapi.MetricSpec{}
		}
		c.Spec.MetricSpec.Enabled = c.Spec.MetricsSpec.Enabled
	}

	if len(c.Spec.MetricsSpec.Rules) > 0 {
		if c.Spec.MetricSpec == nil {
			c.Spec.MetricSpec = &configurationapi.MetricSpec{}
		}
		c.Spec.MetricSpec.Rules = c.Spec.MetricsSpec.Rules
	}

	if c.Spec.MetricsSpec.HTTP != nil {
		if c.Spec.MetricSpec == nil {
			c.Spec.MetricSpec = &configurationapi.MetricSpec{}
		}
		c.Spec.MetricSpec.HTTP = c.Spec.MetricsSpec.HTTP
	}
}

func (a *apiServer) overrideConfigDefaults(ctx context.Context, cfg *configurationapi.Configuration) error {
	// override Metric Spec with Dapr System Config if not set (check both forms of metric(s)Spec )
	if (cfg.Spec.MetricSpec == nil || cfg.Spec.MetricSpec.HTTP == nil) && (cfg.Spec.MetricsSpec == nil || cfg.Spec.MetricsSpec.HTTP == nil) {
		daprSystemConfig, err := a.getDaprSystemConfiguration(ctx)
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
		if err == nil { // skip if not found
			sortMetricsSpec(daprSystemConfig)
			if daprSystemConfig.Spec.MetricSpec != nil && daprSystemConfig.Spec.MetricSpec.HTTP != nil {
				if cfg.Spec.MetricSpec == nil {
					cfg.Spec.MetricSpec = &configurationapi.MetricSpec{}
				}
				cfg.Spec.MetricSpec.HTTP = daprSystemConfig.Spec.MetricSpec.HTTP
			}
		}
	}
	return nil
}

// GetConfiguration returns a Dapr configuration.
func (a *apiServer) GetConfiguration(ctx context.Context, in *operatorv1pb.GetConfigurationRequest) (*operatorv1pb.GetConfigurationResponse, error) {
	if _, err := authz.Request(ctx, in.GetNamespace()); err != nil {
		return nil, err
	}

	key := types.NamespacedName{Namespace: in.GetNamespace(), Name: in.GetName()}
	var config configurationapi.Configuration
	if err := a.Client.Get(ctx, key, &config); err != nil {
		return nil, fmt.Errorf("error getting configuration %s/%s: %w", in.GetNamespace(), in.GetName(), err)
	}

	if err := a.overrideConfigDefaults(ctx, &config); err != nil {
		return nil, fmt.Errorf("error overriding defaults: %w", err)
	}

	b, err := json.Marshal(&config)
	if err != nil {
		return nil, fmt.Errorf("error marshalling configuration: %w", err)
	}

	return &operatorv1pb.GetConfigurationResponse{
		Configuration: b,
	}, nil
}
