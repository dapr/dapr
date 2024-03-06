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

package loader

import (
	"context"
	"io"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/runtime/hotreload/differ"
)

// Interface is an interface for loading and watching for changes to components
// a source.
type Interface interface {
	io.Closer
	Components() Loader[componentsapi.Component]
}

// Loader is an interface for loading and watching for changes to a resource
// from a source.
type Loader[T differ.Resource] interface {
	List(context.Context) (*differ.LocalRemoteResources[T], error)
	Stream(context.Context) (<-chan *Event[T], error)
}

// Event is a component event.
type Event[T differ.Resource] struct {
	Type     operatorv1pb.ResourceEventType
	Resource T
}
