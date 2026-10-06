//go:build unit

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

package binarystore_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	contribbinarystore "github.com/dapr/components-contrib/binarystore"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compbinarystore "github.com/dapr/dapr/pkg/components/binarystore"
	"github.com/dapr/dapr/pkg/runtime/processor/binarystore"
	"github.com/dapr/kit/logger"
)

func TestInitNilStore(t *testing.T) {
	registry := compbinarystore.NewRegistry()
	registry.RegisterComponent(func(logger.Logger) contribbinarystore.BinaryStore {
		return nil
	}, "nil")

	comp := compapi.Component{}
	comp.Name = "nil-store"
	comp.Spec.Type = "binarystore.nil"
	comp.Spec.Version = "v1"

	err := binarystore.New(binarystore.Options{Registry: registry}).Init(t.Context(), comp)
	require.EqualError(t, err,
		"[CREATE_COMPONENT_FAILURE]: initialization error occurred for "+
			comp.LogName()+": binary store binarystore.nil/v1 returned a nil store")
}
