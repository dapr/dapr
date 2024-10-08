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

package mock

import (
	"context"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/secretstores"
)

type SecretStore struct {
	secretstores.SecretStore
	CloseErr error
}

func (s *SecretStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (s *SecretStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	return nil
}

func (s *SecretStore) Close() error {
	return s.CloseErr
}

var TestInputBindingData = []byte("fakedata")

type Binding struct {
	ReadErrorCh chan bool
	Data        string
	Metadata    map[string]string
	CloseErr    error
}

func (b *Binding) Init(ctx context.Context, metadata bindings.Metadata) error {
	return nil
}

func (b *Binding) Read(ctx context.Context, handler bindings.Handler) error {
	b.Data = string(TestInputBindingData)
	metadata := map[string]string{}
	if b.Metadata != nil {
		metadata = b.Metadata
	}

	resp := &bindings.ReadResponse{
		Metadata: metadata,
		Data:     []byte(b.Data),
	}

	if b.ReadErrorCh != nil {
		go func() {
			_, err := handler(ctx, resp)
			b.ReadErrorCh <- (err != nil)
		}()

		return nil
	}

	_, err := handler(ctx, resp)
	return err
}

func (b *Binding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{bindings.CreateOperation, bindings.ListOperation}
}

func (b *Binding) Invoke(ctx context.Context, req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	return nil, nil
}

func (b *Binding) Close() error {
	return b.CloseErr
}

type MockKubernetesStateStore struct {
	Callback func(context.Context) error
	CloseFn  func() error
}

func (m *MockKubernetesStateStore) Init(ctx context.Context, metadata secretstores.Metadata) error {
	if m.Callback != nil {
		return m.Callback(ctx)
	}
	return nil
}

func (m *MockKubernetesStateStore) GetSecret(ctx context.Context, req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func (m *MockKubernetesStateStore) BulkGetSecret(ctx context.Context, req secretstores.BulkGetSecretRequest) (secretstores.BulkGetSecretResponse, error) {
	response := map[string]map[string]string{}
	response["k8s-secret"] = map[string]string{
		"key1":   "value1",
		"_value": "_value_data",
		"name1":  "value1",
	}
	return secretstores.BulkGetSecretResponse{
		Data: response,
	}, nil
}

func (m *MockKubernetesStateStore) Close() error {
	if m.CloseFn != nil {
		return m.CloseFn()
	}
	return nil
}

func (m *MockKubernetesStateStore) Features() []secretstores.Feature {
	return []secretstores.Feature{}
}

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func NewMockKubernetesStoreWithInitCallback(cb func(context.Context) error) secretstores.SecretStore {
	return &MockKubernetesStateStore{Callback: cb}
}

func NewMockKubernetesStoreWithClose(closeFn func() error) secretstores.SecretStore {
	return &MockKubernetesStateStore{CloseFn: closeFn}
}
