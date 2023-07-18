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

	"github.com/kelseyhightower/envconfig"
	corev1 "k8s.io/api/core/v1"

	"github.com/dapr/dapr/utils"
)

// Config represents configuration options for the Dapr Sidecar Injector webhook server.
type Config struct {
	TLSCertFile                       string `envconfig:"TLS_CERT_FILE" required:"true"`
	TLSKeyFile                        string `envconfig:"TLS_KEY_FILE" required:"true"`
	SidecarImage                      string `envconfig:"SIDECAR_IMAGE" required:"true"`
	SidecarImagePullPolicy            string `envconfig:"SIDECAR_IMAGE_PULL_POLICY"`
	Namespace                         string `envconfig:"NAMESPACE" required:"true"`
	KubeClusterDomain                 string `envconfig:"KUBE_CLUSTER_DOMAIN"`
	AllowedServiceAccounts            string `envconfig:"ALLOWED_SERVICE_ACCOUNTS"`
	AllowedServiceAccountsPrefixNames string `envconfig:"ALLOWED_SERVICE_ACCOUNTS_PREFIX_NAMES"`
	IgnoreEntrypointTolerations       string `envconfig:"IGNORE_ENTRYPOINT_TOLERATIONS"`
	SkipPlacement                     string `envconfig:"SKIP_PLACEMENT"`
	RunAsNonRoot                      string `envconfig:"SIDECAR_RUN_AS_NON_ROOT"`
	ReadOnlyRootFilesystem            string `envconfig:"SIDECAR_READ_ONLY_ROOT_FILESYSTEM"`
	SidecarDropALLCapabilities        string `envconfig:"SIDECAR_DROP_ALL_CAPABILITIES"`

	parsedEntrypointTolerations []corev1.Toleration
}

// NewConfigWithDefaults returns a Config object with default values already
// applied. Callers are then free to set custom values for the remaining fields
// and/or override default values.
func NewConfigWithDefaults() Config {
	return Config{
		SidecarImagePullPolicy: "Always",
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
		clusterDomain, err := utils.GetKubeClusterDomain()
		if err != nil {
			log.Errorf("Failed to get clusterDomain err:%s, set default:%s", err, utils.DefaultKubeClusterDomain)
			c.KubeClusterDomain = utils.DefaultKubeClusterDomain
		} else {
			c.KubeClusterDomain = clusterDomain
		}
	}

	c.parseTolerationsJSON()

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

func (c *Config) GetIgnoreEntrypointTolerations() []corev1.Toleration {
	return c.parsedEntrypointTolerations
}

func (c *Config) GetRunAsNonRoot() bool {
	// Default is true if empty
	if c.RunAsNonRoot == "" {
		return true
	}
	return utils.IsTruthy(c.RunAsNonRoot)
}

func (c *Config) GetReadOnlyRootFilesystem() bool {
	// Default is true if empty
	if c.ReadOnlyRootFilesystem == "" {
		return true
	}
	return utils.IsTruthy(c.ReadOnlyRootFilesystem)
}

func (c *Config) GetDropCapabilities() bool {
	// Default is false if empty
	return utils.IsTruthy(c.SidecarDropALLCapabilities)
}

func (c *Config) GetSkipPlacement() bool {
	// Default is false if empty
	return utils.IsTruthy(c.SkipPlacement)
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
