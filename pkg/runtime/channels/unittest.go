//go:build unit
// +build unit

/*
Copyright 2023 The Dapr Authors
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

package channels

import "github.com/dapr/dapr/pkg/channel"

// WithAppChannel is used for testing to override the underlying app channel.
func (c *Channels) WithAppChannel(appChannel channel.AppChannel) *Channels {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.appChannel = appChannel
	return c
}

// WithEndpointChannels is used for testing to override the underlying endpoint
// channels.
func (c *Channels) WithEndpointChannels(endpChannels map[string]channel.HTTPEndpointAppChannel) *Channels {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.endpChannels = endpChannels
	return c
}
