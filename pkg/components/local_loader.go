/*
Copyright 2021 The Dapr Authors
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

package components

import (
	"errors"
	"fmt"
	"os"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/security"
)

// LocalComponents loads components from a given directory.
type LocalComponents struct {
	componentsManifestLoader ManifestLoader[componentsV1alpha1.Component]
	namespace                string
}

// NewLocalComponents returns a new LocalComponents.
func NewLocalComponents(resourcesPaths ...string) *LocalComponents {
	return &LocalComponents{
		componentsManifestLoader: NewDiskManifestLoader[componentsV1alpha1.Component](resourcesPaths...),
		namespace:                security.CurrentNamespace(),
	}
}

// Load loads dapr components from a given directory.
func (s *LocalComponents) Load() ([]componentsV1alpha1.Component, error) {
	comps, err := s.componentsManifestLoader.Load()
	if err != nil {
		return nil, err
	}

	nsDefined := len(os.Getenv("NAMESPACE")) != 0

	names := make(map[string]string)
	var errs []error
	for i := range comps {
		// If the process or component namespace are not defined, use the
		// current defaulted security namespace.
		if !nsDefined || len(comps[i].Namespace) == 0 {
			comps[i].Namespace = s.namespace
		}

		// Ignore components that are not in the process security namespace.
		if comps[i].Namespace != s.namespace {
			continue
		}

		if existing, ok := names[comps[i].Name]; ok {
			errs = append(errs, fmt.Errorf("duplicate definition of component name %s with existing %s", comps[i].LogName(), existing))
			continue
		}

		names[comps[i].Name] = comps[i].LogName()
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return comps, nil
}
