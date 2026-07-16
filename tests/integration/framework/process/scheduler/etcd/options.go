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

package etcd

type Option func(*options)

type options struct {
	username *string
	password *string

	nodes int
}

func WithUsername(username string) Option {
	return func(o *options) {
		o.username = &username
	}
}

func WithPassword(password string) Option {
	return func(o *options) {
		o.password = &password
	}
}

func WithNodes(nodes int) Option {
	return func(o *options) {
		o.nodes = nodes
	}
}
