// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package injector

import (
	"github.com/kelseyhightower/envconfig"

	"github.com/dapr/dapr/utils"
)

// Config represents configuration options for the Dapr Sidecar Injector webhook server.
type Config struct {
	TLSCertFile            string `envconfig:"TLS_CERT_FILE" required:"true"`
	TLSKeyFile             string `envconfig:"TLS_KEY_FILE" required:"true"`
	SidecarImage           string `envconfig:"SIDECAR_IMAGE" required:"true"`
	SidecarImagePullPolicy string `envconfig:"SIDECAR_IMAGE_PULL_POLICY"`
	Namespace              string `envconfig:"NAMESPACE" required:"true"`
	KubeClusterDomain      string `envconfig:"KUBE_CLUSTER_DOMAIN"`
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
			log.Errorf("failed to get clusterDomain err:%s, set default:%s", err, utils.DefaultKubeClusterDomain)
			c.KubeClusterDomain = utils.DefaultKubeClusterDomain
		} else {
			c.KubeClusterDomain = clusterDomain
		}
	}
	return c, nil
}
