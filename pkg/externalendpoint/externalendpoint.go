package externalendpoint

import (
	"os"
	"path/filepath"

	externalHTTPEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/externalHTTPEndpoint/v1alpha1"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gopkg.in/yaml.v2"
)

const (
	externalEndpointKind = "ExternalHTTPEndpoint"
)

// LoadLocalExternalEndpoint loads external endpoint configurations from local folders.
func LoadLocalExternalEndpoint(log logger.Logger, runtimeID string, paths ...string) []*externalHTTPEndpointV1alpha1.ExternalHTTPEndpoint {
	configs := []*externalHTTPEndpointV1alpha1.ExternalHTTPEndpoint{}
	for _, path := range paths {
		loaded := loadLocalExternalHTTPEndpointPath(log, runtimeID, path)
		if len(loaded) > 0 {
			configs = append(configs, loaded...)
		}
	}
	return configs
}

func loadLocalExternalHTTPEndpointPath(log logger.Logger, runtimeID string, path string) []*externalHTTPEndpointV1alpha1.ExternalHTTPEndpoint {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}

	files, err := os.ReadDir(path)
	if err != nil {
		log.Errorf("Failed to read external http endpoint files from path %s: %v", path, err)
		return nil
	}

	configs := make([]*externalHTTPEndpointV1alpha1.ExternalHTTPEndpoint, 0, len(files))

	type typeInfo struct {
		metav1.TypeMeta `json:",inline"`
	}

	for _, file := range files {
		if !utils.IsYaml(file.Name()) {
			log.Warnf("A non-YAML external http endpoint file %s was detected, it will not be loaded", file.Name())
			continue
		}
		filePath := filepath.Join(path, file.Name())
		b, err := os.ReadFile(filePath)
		if err != nil {
			log.Errorf("Could not read external http endpoint file %s: %v", file.Name(), err)
			continue
		}

		var ti typeInfo
		if err = yaml.Unmarshal(b, &ti); err != nil {
			log.Errorf("Could not determine resource type: %v", err)
			continue
		}

		if ti.Kind != externalEndpointKind {
			log.Errorf("invalid kind here %s", ti)
			continue
		}

		var endpoint externalHTTPEndpointV1alpha1.ExternalHTTPEndpoint
		if err = yaml.Unmarshal(b, &endpoint); err != nil {
			log.Errorf("Could not parse external http endpoint file %s: %v", file.Name(), err)
			continue
		}
		configs = append(configs, &endpoint)
	}

	return configs
}
