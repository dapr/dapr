// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package v1

import (
	"testing"

	structpb "github.com/golang/protobuf/ptypes/struct"
	"github.com/stretchr/testify/assert"
)

func TestInternalMetadataToHTTPHeader(t *testing.T) {
	testValue := &structpb.ListValue{
		Values: []*structpb.Value{
			{
				Kind: &structpb.Value_StringValue{StringValue: "fakeValue"},
			},
		},
	}

	fakeMetadata := map[string]*structpb.ListValue{
		"custom-header":  testValue,
		":method":        testValue,
		":scheme":        testValue,
		":path":          testValue,
		":authority":     testValue,
		"grpc-timeout":   testValue,
		"content-type":   testValue, // skip
		"grpc-trace-bin": testValue, // skip binary metadata
	}

	expectedKeyNames := []string{"custom-header", "dapr-:method", "dapr-:scheme", "dapr-:path", "dapr-:authority", "dapr-grpc-timeout"}
	savedHeaderKeyNames := []string{}
	InternalMetadataToHTTPHeader(fakeMetadata, func(k, v string) {
		savedHeaderKeyNames = append(savedHeaderKeyNames, k)
	})

	assert.Equal(t, expectedKeyNames, savedHeaderKeyNames)
}
