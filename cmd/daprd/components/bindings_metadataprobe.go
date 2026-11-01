//go:build bindings_metadataprobe

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

package components

import (
	"context"
	"encoding/json"

	contribbindings "github.com/dapr/components-contrib/bindings"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	"github.com/dapr/kit/logger"
)

// metadataProbeBinding is an integration-test-only output binding that echoes
// the request metadata it receives as JSON in the response data, letting a
// test assert exactly which metadata daprd forwards to a component.
//
// It is gated behind the bindings_metadataprobe build tag, set only for the
// integration-test daprd binary and never for a released flavor, so it is
// never shipped.
type metadataProbeBinding struct{}

func (m *metadataProbeBinding) Init(context.Context, contribbindings.Metadata) error {
	return nil
}

func (m *metadataProbeBinding) Close() error {
	return nil
}

func (m *metadataProbeBinding) Operations() []contribbindings.OperationKind {
	return []contribbindings.OperationKind{contribbindings.GetOperation}
}

func (m *metadataProbeBinding) Invoke(_ context.Context, req *contribbindings.InvokeRequest) (*contribbindings.InvokeResponse, error) {
	data, err := json.Marshal(req.Metadata)
	if err != nil {
		return nil, err
	}
	return &contribbindings.InvokeResponse{Data: data}, nil
}

func init() {
	bindingsLoader.DefaultRegistry.RegisterOutputBinding(func(logger.Logger) contribbindings.OutputBinding {
		return &metadataProbeBinding{}
	}, "metadataprobe")
}
