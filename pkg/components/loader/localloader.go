/*
Copyright 2025 The Dapr Authors
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

package loader

import (
	"context"

	"github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	internalloader "github.com/dapr/dapr/pkg/internal/loader"
	"github.com/dapr/dapr/pkg/internal/loader/disk"
)

// LocalLoader Exposes the internal Components loader for CLI to use as validation of components loading from the local file system
type LocalLoader struct {
	loader internalloader.Loader[v1alpha1.Component]
}

func NewLocalLoader(appID string, paths []string) *LocalLoader {
	l := disk.NewComponents(disk.Options{
		AppID: appID,
		Paths: paths,
	})
	return &LocalLoader{
		loader: l,
	}
}

// Load loads components from the local file system, used for testing
func (l *LocalLoader) Load(ctx context.Context) ([]v1alpha1.Component, error) {
	components, err := l.loader.Load(ctx)
	if err != nil {
		return nil, err
	}
	return components, nil
}

func (l *LocalLoader) Validate(ctx context.Context) error {
	// Dismiss the manifests as we are only interested in the error returned by the loader as indication of any component loading errors
	_, err := l.loader.Load(ctx)
	if err != nil {
		return err
	}
	return nil
}
