/*
Copyright 2022 The Dapr Authors
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

package service

import (
	"encoding/json"
	"fmt"

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/pkg/injector/patcher"
	"github.com/dapr/dapr/utils"
	kitutils "github.com/dapr/kit/utils"
)

// Config represents configuration options for the Dapr Sidecar Injector webhook server.
type Config struct {
	SidecarImage                      string `envconfig:"SIDECAR_IMAGE" required:"true"`
	SidecarImagePullPolicy            string `envconfig:"SIDECAR_IMAGE_PULL_POLICY"`
	Namespace                         string `envconfig:"NAMESPACE" required:"true"`
	KubeClusterDomain                 string `envconfig:"KUBE_CLUSTER_DOMAIN"`
	AllowedServiceAccounts            string `envconfig:"ALLOWED_SERVICE_ACCOUNTS"`
	AllowedServiceAccountsPrefixNames string `envconfig:"ALLOWED_SERVICE_ACCOUNTS_PREFIX_NAMES"`
	IgnoreEntrypointTolerations       string `envconfig:"IGNORE_ENTRYPOINT_TOLERATIONS"`
	ActorsEnabled                     string `envconfig:"ACTORS_ENABLED"`
	ActorsServiceName                 string `envconfig:"ACTORS_SERVICE_NAME"`
	ActorsServiceAddress              string `envconfig:"ACTORS_SERVICE_ADDRESS"`
	RemindersServiceName              string `envconfig:"REMINDERS_SERVICE_NAME"`
	RemindersServiceAddress           string `envconfig:"REMINDERS_SERVICE_ADDRESS"`
	RunAsNonRoot                      string `envconfig:"SIDECAR_RUN_AS_NON_ROOT"`
	ReadOnlyRootFilesystem            string `envconfig:"SIDECAR_READ_ONLY_ROOT_FILESYSTEM"`
	SidecarDropALLCapabilities        string `envconfig:"SIDECAR_DROP_ALL_CAPABILITIES"`

	TrustAnchorsFile        string `envconfig:"DAPR_TRUST_ANCHORS_FILE"`
	ControlPlaneTrustDomain string `envconfig:"DAPR_CONTROL_PLANE_TRUST_DOMAIN"`
	SentryAddress           string `envconfig:"DAPR_SENTRY_ADDRESS"`

	parsedActorsEnabled              bool
	parsedActorsService              patcher.Service
	parsedRemindersService           patcher.Service
	parsedRunAsNonRoot               bool
	parsedReadOnlyRootFilesystem     bool
	parsedSidecarDropALLCapabilities bool
	parsedEntrypointTolerations      []corev1.Toleration
}

// NewConfigWithDefaults returns a Config object with default values already
// applied. Callers are then free to set custom values for the remaining fields
// and/or override default values.
func NewConfigWithDefaults() Config {
	return Config{
		SidecarImagePullPolicy:  "Always",
		ControlPlaneTrustDomain: "cluster.local",
		TrustAnchorsFile:        "/var/run/dapr.io/tls/ca.crt",
	}
}

// GetConfig returns configuration derived from environment variables.
func GetConfig() (Config, error) {
	// get config from environment variables
	c := NewConfigWithDefaults()
	err := envconfig.Process("", &c)
	if err != nil {
		return c, err
	}

	if c.KubeClusterDomain == "" {
		// auto-detect KubeClusterDomain from resolv.conf file
		var clusterDomain string
		clusterDomain, err = utils.GetKubeClusterDomain()
		if err != nil {
			log.Errorf("Failed to get clusterDomain err:%s, set default:%s", err, utils.DefaultKubeClusterDomain)
			c.KubeClusterDomain = utils.DefaultKubeClusterDomain
		} else {
			c.KubeClusterDomain = clusterDomain
		}
	}

	err = c.parse()
	if err != nil {
		return c, fmt.Errorf("failed to parse configuration: %w", err)
	}

	return c, nil
}

func (c Config) GetPullPolicy() corev1.PullPolicy {
	switch c.SidecarImagePullPolicy {
	case "Always":
		return corev1.PullAlways
	case "Never":
		return corev1.PullNever
	case "IfNotPresent":
		return corev1.PullIfNotPresent
	default:
		return corev1.PullIfNotPresent
	}
}

func (c Config) GetIgnoreEntrypointTolerations() []corev1.Toleration {
	return c.parsedEntrypointTolerations
}

func (c Config) GetRunAsNonRoot() bool {
	return c.parsedRunAsNonRoot
}

func (c Config) GetReadOnlyRootFilesystem() bool {
	return c.parsedReadOnlyRootFilesystem
}

func (c Config) GetDropCapabilities() bool {
	return c.parsedSidecarDropALLCapabilities
}

func (c Config) GetActorsEnabled() bool {
	return c.parsedActorsEnabled
}

func (c Config) GetActorsService() (string, patcher.Service) {
	return c.ActorsServiceName, c.parsedActorsService
}

// GetRemindersService returns the configured reminders service.
// The returned boolean value will be false if the configuration uses the built-in reminders subsystem
func (c Config) GetRemindersService() (string, patcher.Service, bool) {
	if c.RemindersServiceName == "" {
		return "", patcher.Service{}, false
	}
	return c.RemindersServiceName, c.parsedRemindersService, true
}

func (c *Config) parse() (err error) {
	// If there's no configuration for the actors service, use the traditional placement
	if c.ActorsServiceName == "" {
		c.ActorsServiceName = "placement"
		c.parsedActorsService = patcher.ServicePlacement
	} else {
		c.parsedActorsService, err = patcher.NewService(c.ActorsServiceAddress)
		if err != nil {
			return fmt.Errorf("invalid value for actor service address: %w", err)
		}
	}

	// If the name of the reminders service is empty, we assume we are using the built-in capabilities
	if c.RemindersServiceName != "" && c.RemindersServiceName != "default" {
		c.parsedRemindersService, err = patcher.NewService(c.RemindersServiceAddress)
		if err != nil {
			return fmt.Errorf("invalid value for reminder service address: %w", err)
		}
	}

	// Parse the tolerations as JSON
	c.parseTolerationsJSON()

	// Set some booleans
	c.parsedActorsEnabled = isTruthyDefaultTrue(c.ActorsEnabled)
	c.parsedRunAsNonRoot = isTruthyDefaultTrue(c.RunAsNonRoot)
	c.parsedReadOnlyRootFilesystem = isTruthyDefaultTrue(c.ReadOnlyRootFilesystem)
	c.parsedSidecarDropALLCapabilities = kitutils.IsTruthy(c.SidecarDropALLCapabilities)

	return nil
}

func (c *Config) parseTolerationsJSON() {
	if c.IgnoreEntrypointTolerations == "" {
		return
	}

	// If the string contains an invalid value, log a warning and continue.
	ts := []corev1.Toleration{}
	err := json.Unmarshal([]byte(c.IgnoreEntrypointTolerations), &ts)
	if err != nil {
		log.Warnf("Couldn't parse entrypoint tolerations (%s): %v", c.IgnoreEntrypointTolerations, err)
		return
	}

	c.parsedEntrypointTolerations = ts
}

func isTruthyDefaultTrue(val string) bool {
	// Default is true if empty
	if val == "" {
		return true
	}
	return kitutils.IsTruthy(val)
}
