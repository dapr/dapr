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
	"errors"
	"fmt"
	"os"

	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
)

// disk loads a specific manifest kind from a folder.
type disk[T meta.Resource] struct {
	kind       string
	apiVersion string
	dirs       []string
	namespace  string
	appID      string
}

type Options struct {
	AppID string
	Paths []string
}

// new creates a new manifest loader for the given paths and kind.
func new[T meta.Resource](opts Options) *disk[T] {
	var zero T
	return &disk[T]{
		dirs:       opts.Paths,
		kind:       zero.Kind(),
		apiVersion: zero.APIVersion(),
		namespace:  security.CurrentNamespace(),
		appID:      opts.AppID,
	}
}

// load loads manifests for the given directory.
func (d *disk[T]) Load(context.Context) ([]T, error) {
	set, err := d.loadWithOrder()
	if err != nil {
		return nil, err
	}

	nsDefined := len(os.Getenv("NAMESPACE")) != 0

	names := make(map[string]string)
	filteredManifests := make([]T, 0)
	var errs []error
	for i := range set.ts {
		// If the process or manifest namespace are not defined, ignore the
		// manifest namespace.
		ignoreNamespace := !nsDefined || len(set.ts[i].GetNamespace()) == 0

		// Ignore manifests that are not in the process security namespace.
		if !ignoreNamespace && set.ts[i].GetNamespace() != d.namespace {
			continue
		}

		if existing, ok := names[set.ts[i].GetName()]; ok {
			errs = append(errs, fmt.Errorf("duplicate definition of %s name %s with existing %s",
				set.ts[i].Kind(), set.ts[i].LogName(), existing))
			continue
		}

		// Skip manifests which are not in scope
		scopes := set.ts[i].GetScopes()
		if !(len(scopes) == 0 || utils.Contains(scopes, d.appID)) {
			continue
		}

		names[set.ts[i].GetName()] = set.ts[i].LogName()
		filteredManifests = append(filteredManifests, set.ts[i])
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return filteredManifests, nil
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
