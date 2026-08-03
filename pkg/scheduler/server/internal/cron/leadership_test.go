/*
Copyright 2026 The Dapr Authors
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

package cron

import (
	"testing"

	"github.com/stretchr/testify/assert"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func TestPlacementLeader(t *testing.T) {
	t.Parallel()

	host := func(addr string, placement bool) *schedulerv1pb.Host {
		return &schedulerv1pb.Host{Address: addr, PlacementEnabled: placement}
	}

	tests := map[string]struct {
		hosts []*schedulerv1pb.Host
		exp   string
	}{
		"no hosts": {
			hosts: nil,
			exp:   "",
		},
		"no capable hosts": {
			hosts: []*schedulerv1pb.Host{host("a:1", false), host("b:1", false)},
			exp:   "",
		},
		"first sorted capable host wins": {
			hosts: []*schedulerv1pb.Host{host("c:1", true), host("a:1", true), host("b:1", true)},
			exp:   "a:1",
		},
		"incapable hosts are skipped even when sorted first": {
			hosts: []*schedulerv1pb.Host{host("a:1", false), host("c:1", true), host("b:1", true)},
			exp:   "b:1",
		},
		"single capable host": {
			hosts: []*schedulerv1pb.Host{host("z:1", true)},
			exp:   "z:1",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, test.exp, placementLeader(test.hosts))
		})
	}
}
