/*
Copyright 2022 The Dapr Authors
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

package universalapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/secretstores"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/messages"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestSecretStoreNotConfigured(t *testing.T) {
	// Setup Dapr API
	fakeAPI := &UniversalAPI{
		Logger: testLogger,
	}

	// act
	t.Run("GetSecret", func(t *testing.T) {
		_, err := fakeAPI.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrSecretStoreNotConfigured)
	})

	t.Run("GetBulkSecret", func(t *testing.T) {
		_, err := fakeAPI.GetBulkSecret(context.Background(), &runtimev1pb.GetBulkSecretRequest{})
		require.Error(t, err)
		assert.ErrorIs(t, err, messages.ErrSecretStoreNotConfigured)
	})
}

func TestGetSecret(t *testing.T) {
	fakeStore := daprt.FakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
		"store2": fakeStore,
		"store3": fakeStore,
		"store4": fakeStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
		"store2": {
			DefaultAccess:  config.DenyAccess,
			AllowedSecrets: []string{"good-key"},
		},
		"store3": {
			DefaultAccess:  config.AllowAccess,
			AllowedSecrets: []string{"error-key", "good-key"},
		},
	}
	expectedResponse := "life is good"
	storeName := "store1"
	deniedStoreName := "store2"
	restrictedStore := "store3"
	unrestrictedStore := "store4"     // No configuration defined for the store
	nonExistingStore := "nonexistent" // Non-existing store

	testCases := []struct {
		testName         string
		storeName        string
		key              string
		errorExcepted    bool
		expectedResponse string
		expectedError    codes.Code
	}{
		{
			testName:         "Good Key from unrestricted store",
			storeName:        unrestrictedStore,
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key default access",
			storeName:        storeName,
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Good Key restricted store access",
			storeName:        restrictedStore,
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
		{
			testName:         "Error Key restricted store access",
			storeName:        restrictedStore,
			key:              "error-key",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.Internal,
		},
		{
			testName:         "Random Key restricted store access",
			storeName:        restrictedStore,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Random Key accessing a store denied access by default",
			storeName:        deniedStoreName,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Random Key accessing a store denied access by default",
			storeName:        deniedStoreName,
			key:              "random",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.PermissionDenied,
		},
		{
			testName:         "Store doesn't exist",
			storeName:        nonExistingStore,
			key:              "key",
			errorExcepted:    true,
			expectedResponse: "",
			expectedError:    codes.InvalidArgument,
		},
	}

	// Setup Dapr API
	fakeAPI := &UniversalAPI{
		Logger:               testLogger,
		Resiliency:           resiliency.New(nil),
		SecretStores:         fakeStores,
		SecretsConfiguration: secretsConfiguration,
	}

	// act
	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetSecretRequest{
				StoreName: tt.storeName,
				Key:       tt.key,
			}
			resp, err := fakeAPI.GetSecret(context.Background(), req)

			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Data[tt.key], tt.expectedResponse, "Expected responses to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestGetBulkSecret(t *testing.T) {
	fakeStore := daprt.FakeSecretStore{}
	fakeStores := map[string]secretstores.SecretStore{
		"store1": fakeStore,
	}
	secretsConfiguration := map[string]config.SecretsScope{
		"store1": {
			DefaultAccess: config.AllowAccess,
			DeniedSecrets: []string{"not-allowed"},
		},
	}
	expectedResponse := "life is good"

	testCases := []struct {
		testName         string
		storeName        string
		key              string
		errorExcepted    bool
		expectedResponse string
		expectedError    codes.Code
	}{
		{
			testName:         "Good Key from unrestricted store",
			storeName:        "store1",
			key:              "good-key",
			errorExcepted:    false,
			expectedResponse: expectedResponse,
		},
	}

	// Setup Dapr API
	fakeAPI := &UniversalAPI{
		Logger:               testLogger,
		Resiliency:           resiliency.New(nil),
		SecretStores:         fakeStores,
		SecretsConfiguration: secretsConfiguration,
	}

	// act
	for _, tt := range testCases {
		t.Run(tt.testName, func(t *testing.T) {
			req := &runtimev1pb.GetBulkSecretRequest{
				StoreName: tt.storeName,
			}
			resp, err := fakeAPI.GetBulkSecret(context.Background(), req)

			if !tt.errorExcepted {
				assert.NoError(t, err, "Expected no error")
				assert.Equal(t, resp.Data[tt.key].Secrets[tt.key], tt.expectedResponse, "Expected responses to be same")
			} else {
				assert.Error(t, err, "Expected error")
				assert.Equal(t, tt.expectedError, status.Code(err))
			}
		})
	}
}

func TestSecretAPIWithResiliency(t *testing.T) {
	failingStore := daprt.FailingSecretStore{
		Failure: daprt.NewFailure(
			map[string]int{"key": 1, "bulk": 1},
			map[string]time.Duration{"timeout": time.Second * 10, "bulkTimeout": time.Second * 10},
			map[string]int{},
		),
	}

	// Setup Dapr API
	fakeAPI := &UniversalAPI{
		Logger:       testLogger,
		Resiliency:   resiliency.FromConfigurations(testLogger, testResiliency),
		SecretStores: map[string]secretstores.SecretStore{"failSecret": failingStore},
	}

	// act
	t.Run("Get secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := fakeAPI.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "key",
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("key"))
	})

	t.Run("Get secret - timeout before request ends", func(t *testing.T) {
		// Store sleeps for 10 seconds, let's make sure our timeout takes less time than that.
		start := time.Now()
		_, err := fakeAPI.GetSecret(context.Background(), &runtimev1pb.GetSecretRequest{
			StoreName: "failSecret",
			Key:       "timeout",
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("timeout"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	t.Run("Get bulk secret - retries on initial failure with resiliency", func(t *testing.T) {
		_, err := fakeAPI.GetBulkSecret(context.Background(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulk"},
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulk"))
	})

	t.Run("Get bulk secret - timeout before request ends", func(t *testing.T) {
		start := time.Now()
		_, err := fakeAPI.GetBulkSecret(context.Background(), &runtimev1pb.GetBulkSecretRequest{
			StoreName: "failSecret",
			Metadata:  map[string]string{"key": "bulkTimeout"},
		})
		end := time.Now()

		assert.Error(t, err)
		assert.Equal(t, 2, failingStore.Failure.CallCount("bulkTimeout"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}
