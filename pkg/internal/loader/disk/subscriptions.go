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
	"sort"

	"github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	"github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/internal/loader"
)

type subscriptions struct {
	v1 *disk[v1alpha1.Subscription]
	v2 *disk[v2alpha1.Subscription]
}

func NewSubscriptions(paths ...string) loader.Loader[v2alpha1.Subscription] {
	return &subscriptions{
		v1: new[v1alpha1.Subscription](paths...),
		v2: new[v2alpha1.Subscription](paths...),
	}
}

func (s *subscriptions) Load(context.Context) ([]v2alpha1.Subscription, error) {
	v1, err := s.v1.loadWithOrder()
	if err != nil {
		return nil, err
	}

	v2, err := s.v2.loadWithOrder()
	if err != nil {
		return nil, err
	}

	v2.order = append(v2.order, v1.order...)

	for _, s := range v1.ts {
		var subv2 v2alpha1.Subscription
		if err := subv2.ConvertFrom(s.DeepCopy()); err != nil {
			return nil, err
		}

		v2.ts = append(v2.ts, subv2)
	}

	// Preserve manifest load order between v1 and v2.
	sort.Sort(v2)

	return v2.ts, nil
}
