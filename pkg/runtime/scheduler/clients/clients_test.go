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

package clients

import (
	"testing"

	"github.com/stretchr/testify/assert"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func Test_Next(t *testing.T) {
	client1 := schedulerv1pb.NewSchedulerClient(nil)
	client2 := schedulerv1pb.NewSchedulerClient(nil)
	client3 := schedulerv1pb.NewSchedulerClient(nil)
	client4 := schedulerv1pb.NewSchedulerClient(nil)
	client5 := schedulerv1pb.NewSchedulerClient(nil)
	client6 := schedulerv1pb.NewSchedulerClient(nil)

	clients := []schedulerv1pb.SchedulerClient{
		client1,
		client2,
		client3,
		client4,
		client5,
		client6,
	}
	c := &Clients{
		clients: clients,
	}

	assert.Equal(t, clients, c.All())
	cl := c.Next()
	assert.NotSame(t, client1, cl)
	assert.Same(t, client2, cl)
	assert.NotSame(t, client3, cl)
	assert.NotSame(t, client4, cl)
	assert.NotSame(t, client5, cl)
	assert.NotSame(t, client6, cl)

	assert.Same(t, client3, c.Next())
	assert.Same(t, client4, c.Next())
	assert.Same(t, client5, c.Next())
	assert.Same(t, client6, c.Next())
	assert.Same(t, client1, c.Next())
	assert.Same(t, client2, c.Next())

	c = &Clients{
		clients: []schedulerv1pb.SchedulerClient{client1},
	}
	assert.Same(t, client1, c.Next())
	assert.Same(t, client1, c.Next())
	assert.Same(t, client1, c.Next())
	assert.Same(t, client1, c.Next())
}
