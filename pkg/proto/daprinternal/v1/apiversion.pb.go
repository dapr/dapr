// Code generated by protoc-gen-go. DO NOT EDIT.
// source: dapr/proto/daprinternal/v1/apiversion.proto

package v1

import (
	fmt "fmt"
	proto "github.com/golang/protobuf/proto"
	math "math"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion3 // please upgrade the proto package

// APIVersion represents the version of Dapr API.
// Dapr and DaprClient service versions follows Dapr API version.
// DaprInternal service version is maintained separately.
type APIVersion int32

const (
	APIVersion_UNKNOWN APIVersion = 0
	APIVersion_V1      APIVersion = 1
)

var APIVersion_name = map[int32]string{
	0: "UNKNOWN",
	1: "V1",
}

var APIVersion_value = map[string]int32{
	"UNKNOWN": 0,
	"V1":      1,
}

func (x APIVersion) String() string {
	return proto.EnumName(APIVersion_name, int32(x))
}

func (APIVersion) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_97824f97a2db432b, []int{0}
}

func init() {
	proto.RegisterEnum("dapr.proto.daprinternal.v1.APIVersion", APIVersion_name, APIVersion_value)
}

func init() {
	proto.RegisterFile("dapr/proto/daprinternal/v1/apiversion.proto", fileDescriptor_97824f97a2db432b)
}

var fileDescriptor_97824f97a2db432b = []byte{
	// 135 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0xd2, 0x4e, 0x49, 0x2c, 0x28,
	0xd2, 0x2f, 0x28, 0xca, 0x2f, 0xc9, 0xd7, 0x07, 0x31, 0x33, 0xf3, 0x4a, 0x52, 0x8b, 0xf2, 0x12,
	0x73, 0xf4, 0xcb, 0x0c, 0xf5, 0x13, 0x0b, 0x32, 0xcb, 0x52, 0x8b, 0x8a, 0x33, 0xf3, 0xf3, 0xf4,
	0xc0, 0x0a, 0x84, 0xa4, 0x40, 0x2a, 0x20, 0x6c, 0x3d, 0x64, 0xc5, 0x7a, 0x65, 0x86, 0x5a, 0x8a,
	0x5c, 0x5c, 0x8e, 0x01, 0x9e, 0x61, 0x10, 0xf5, 0x42, 0xdc, 0x5c, 0xec, 0xa1, 0x7e, 0xde, 0x7e,
	0xfe, 0xe1, 0x7e, 0x02, 0x0c, 0x42, 0x6c, 0x5c, 0x4c, 0x61, 0x86, 0x02, 0x8c, 0x4e, 0x06, 0x51,
	0x7a, 0xe9, 0x99, 0x25, 0x19, 0xa5, 0x49, 0x7a, 0xc9, 0xf9, 0xb9, 0x60, 0xdb, 0x20, 0x44, 0x41,
	0x76, 0x3a, 0x76, 0x17, 0x24, 0xb1, 0x81, 0x85, 0x8d, 0x01, 0x01, 0x00, 0x00, 0xff, 0xff, 0x23,
	0x64, 0xcd, 0xea, 0xa6, 0x00, 0x00, 0x00,
}
