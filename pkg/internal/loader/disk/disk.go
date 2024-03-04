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

package disk

import (
	"context"

	"github.com/dapr/dapr/pkg/internal/loader"
)

// disk loads a specific manifest kind from a folder.
type disk[T loader.Manifest] struct {
	kind       string
	apiVersion string
	dirs       []string
}

// New creates a new manifest loader for the given paths and kind.
func New[T loader.Manifest](dirs ...string) *disk[T] {
	var zero T
	return &disk[T]{
		dirs:       dirs,
		kind:       zero.Kind(),
		apiVersion: zero.APIVersion(),
	}
}

// load loads manifests for the given directory.
func (d *disk[T]) Load(context.Context) ([]T, error) {
	set, err := d.loadWithOrder()
	if err != nil {
		return nil, err
	}

	return set.ts, nil
}

func (d *disk[T]) loadWithOrder() (*manifestSet[T], error) {
	set := &manifestSet[T]{d: d}

	for _, dir := range d.dirs {
		if err := set.loadManifestsFromDirectory(dir); err != nil {
			return nil, err
		}
	}

	return set, nil
}
