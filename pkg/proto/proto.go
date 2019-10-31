// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package proto

//go:generate protoc --go_out=plugins=grpc:. dapr/dapr.proto
//go:generate protoc --go_out=plugins=grpc:. daprclient/daprclient.proto
//go:generate protoc --go_out=plugins=grpc:. daprinternal/daprinternal.proto
