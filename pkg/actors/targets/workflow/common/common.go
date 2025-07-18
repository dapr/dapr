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

package common

type ActorTypeBuilder struct {
	ns string
}

func NewActorTypeBuilder(namespace string) *ActorTypeBuilder {
	return &ActorTypeBuilder{
		ns: namespace,
	}
}

func (a *ActorTypeBuilder) Workflow(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".workflow"
}

func (a *ActorTypeBuilder) Activity(appID string) string {
	return "dapr.internal." + a.ns + "." + appID + ".activity"
}
