// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.25.0
// 	protoc        v3.14.0
// source: dapr/proto/placement/v1/placement.proto

package placement

import (
	proto "github.com/golang/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

// This is a compile-time assertion that a sufficiently up-to-date version
// of the legacy proto package is being used.
const _ = proto.ProtoPackageIsVersion4

type PlacementOrder struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Tables    *PlacementTables `protobuf:"bytes,1,opt,name=tables,proto3" json:"tables,omitempty"`
	Operation string           `protobuf:"bytes,2,opt,name=operation,proto3" json:"operation,omitempty"`
}

func (x *PlacementOrder) Reset() {
	*x = PlacementOrder{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[0]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PlacementOrder) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PlacementOrder) ProtoMessage() {}

func (x *PlacementOrder) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[0]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PlacementOrder.ProtoReflect.Descriptor instead.
func (*PlacementOrder) Descriptor() ([]byte, []int) {
	return file_dapr_proto_placement_v1_placement_proto_rawDescGZIP(), []int{0}
}

func (x *PlacementOrder) GetTables() *PlacementTables {
	if x != nil {
		return x.Tables
	}
	return nil
}

func (x *PlacementOrder) GetOperation() string {
	if x != nil {
		return x.Operation
	}
	return ""
}

type PlacementTables struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Entries map[string]*PlacementTable `protobuf:"bytes,1,rep,name=entries,proto3" json:"entries,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	Version string                     `protobuf:"bytes,2,opt,name=version,proto3" json:"version,omitempty"`
}

func (x *PlacementTables) Reset() {
	*x = PlacementTables{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[1]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PlacementTables) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PlacementTables) ProtoMessage() {}

func (x *PlacementTables) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[1]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PlacementTables.ProtoReflect.Descriptor instead.
func (*PlacementTables) Descriptor() ([]byte, []int) {
	return file_dapr_proto_placement_v1_placement_proto_rawDescGZIP(), []int{1}
}

func (x *PlacementTables) GetEntries() map[string]*PlacementTable {
	if x != nil {
		return x.Entries
	}
	return nil
}

func (x *PlacementTables) GetVersion() string {
	if x != nil {
		return x.Version
	}
	return ""
}

type PlacementTable struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Hosts     map[uint64]string `protobuf:"bytes,1,rep,name=hosts,proto3" json:"hosts,omitempty" protobuf_key:"varint,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	SortedSet []uint64          `protobuf:"varint,2,rep,packed,name=sorted_set,json=sortedSet,proto3" json:"sorted_set,omitempty"`
	LoadMap   map[string]*Host  `protobuf:"bytes,3,rep,name=load_map,json=loadMap,proto3" json:"load_map,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	TotalLoad int64             `protobuf:"varint,4,opt,name=total_load,json=totalLoad,proto3" json:"total_load,omitempty"`
}

func (x *PlacementTable) Reset() {
	*x = PlacementTable{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[2]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *PlacementTable) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PlacementTable) ProtoMessage() {}

func (x *PlacementTable) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[2]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PlacementTable.ProtoReflect.Descriptor instead.
func (*PlacementTable) Descriptor() ([]byte, []int) {
	return file_dapr_proto_placement_v1_placement_proto_rawDescGZIP(), []int{2}
}

func (x *PlacementTable) GetHosts() map[uint64]string {
	if x != nil {
		return x.Hosts
	}
	return nil
}

func (x *PlacementTable) GetSortedSet() []uint64 {
	if x != nil {
		return x.SortedSet
	}
	return nil
}

func (x *PlacementTable) GetLoadMap() map[string]*Host {
	if x != nil {
		return x.LoadMap
	}
	return nil
}

func (x *PlacementTable) GetTotalLoad() int64 {
	if x != nil {
		return x.TotalLoad
	}
	return 0
}

type Host struct {
	state         protoimpl.MessageState
	sizeCache     protoimpl.SizeCache
	unknownFields protoimpl.UnknownFields

	Name     string   `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	Port     int64    `protobuf:"varint,2,opt,name=port,proto3" json:"port,omitempty"`
	Load     int64    `protobuf:"varint,3,opt,name=load,proto3" json:"load,omitempty"`
	Entities []string `protobuf:"bytes,4,rep,name=entities,proto3" json:"entities,omitempty"`
	Id       string   `protobuf:"bytes,5,opt,name=id,proto3" json:"id,omitempty"`
}

func (x *Host) Reset() {
	*x = Host{}
	if protoimpl.UnsafeEnabled {
		mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[3]
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		ms.StoreMessageInfo(mi)
	}
}

func (x *Host) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Host) ProtoMessage() {}

func (x *Host) ProtoReflect() protoreflect.Message {
	mi := &file_dapr_proto_placement_v1_placement_proto_msgTypes[3]
	if protoimpl.UnsafeEnabled && x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Host.ProtoReflect.Descriptor instead.
func (*Host) Descriptor() ([]byte, []int) {
	return file_dapr_proto_placement_v1_placement_proto_rawDescGZIP(), []int{3}
}

func (x *Host) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Host) GetPort() int64 {
	if x != nil {
		return x.Port
	}
	return 0
}

func (x *Host) GetLoad() int64 {
	if x != nil {
		return x.Load
	}
	return 0
}

func (x *Host) GetEntities() []string {
	if x != nil {
		return x.Entities
	}
	return nil
}

func (x *Host) GetId() string {
	if x != nil {
		return x.Id
	}
	return ""
}

var File_dapr_proto_placement_v1_placement_proto protoreflect.FileDescriptor

var file_dapr_proto_placement_v1_placement_proto_rawDesc = []byte{
	0x0a, 0x27, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x70, 0x6c, 0x61,
	0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2f, 0x76, 0x31, 0x2f, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d,
	0x65, 0x6e, 0x74, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x12, 0x17, 0x64, 0x61, 0x70, 0x72, 0x2e,
	0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2e,
	0x76, 0x31, 0x22, 0x70, 0x0a, 0x0e, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x4f,
	0x72, 0x64, 0x65, 0x72, 0x12, 0x40, 0x0a, 0x06, 0x74, 0x61, 0x62, 0x6c, 0x65, 0x73, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x28, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x50,
	0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x73, 0x52, 0x06,
	0x74, 0x61, 0x62, 0x6c, 0x65, 0x73, 0x12, 0x1c, 0x0a, 0x09, 0x6f, 0x70, 0x65, 0x72, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x09, 0x6f, 0x70, 0x65, 0x72, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x22, 0xe1, 0x01, 0x0a, 0x0f, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65,
	0x6e, 0x74, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x73, 0x12, 0x4f, 0x0a, 0x07, 0x65, 0x6e, 0x74, 0x72,
	0x69, 0x65, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x35, 0x2e, 0x64, 0x61, 0x70, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74,
	0x2e, 0x76, 0x31, 0x2e, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61, 0x62,
	0x6c, 0x65, 0x73, 0x2e, 0x45, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79,
	0x52, 0x07, 0x65, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x12, 0x18, 0x0a, 0x07, 0x76, 0x65, 0x72,
	0x73, 0x69, 0x6f, 0x6e, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x07, 0x76, 0x65, 0x72, 0x73,
	0x69, 0x6f, 0x6e, 0x1a, 0x63, 0x0a, 0x0c, 0x45, 0x6e, 0x74, 0x72, 0x69, 0x65, 0x73, 0x45, 0x6e,
	0x74, 0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09,
	0x52, 0x03, 0x6b, 0x65, 0x79, 0x12, 0x3d, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02,
	0x20, 0x01, 0x28, 0x0b, 0x32, 0x27, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x50,
	0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x52, 0x05, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0xfe, 0x02, 0x0a, 0x0e, 0x50, 0x6c, 0x61,
	0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x12, 0x48, 0x0a, 0x05, 0x68,
	0x6f, 0x73, 0x74, 0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x32, 0x2e, 0x64, 0x61, 0x70,
	0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e,
	0x74, 0x2e, 0x76, 0x31, 0x2e, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61,
	0x62, 0x6c, 0x65, 0x2e, 0x48, 0x6f, 0x73, 0x74, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x05,
	0x68, 0x6f, 0x73, 0x74, 0x73, 0x12, 0x1d, 0x0a, 0x0a, 0x73, 0x6f, 0x72, 0x74, 0x65, 0x64, 0x5f,
	0x73, 0x65, 0x74, 0x18, 0x02, 0x20, 0x03, 0x28, 0x04, 0x52, 0x09, 0x73, 0x6f, 0x72, 0x74, 0x65,
	0x64, 0x53, 0x65, 0x74, 0x12, 0x4f, 0x0a, 0x08, 0x6c, 0x6f, 0x61, 0x64, 0x5f, 0x6d, 0x61, 0x70,
	0x18, 0x03, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x34, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31,
	0x2e, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x54, 0x61, 0x62, 0x6c, 0x65, 0x2e,
	0x4c, 0x6f, 0x61, 0x64, 0x4d, 0x61, 0x70, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x07, 0x6c, 0x6f,
	0x61, 0x64, 0x4d, 0x61, 0x70, 0x12, 0x1d, 0x0a, 0x0a, 0x74, 0x6f, 0x74, 0x61, 0x6c, 0x5f, 0x6c,
	0x6f, 0x61, 0x64, 0x18, 0x04, 0x20, 0x01, 0x28, 0x03, 0x52, 0x09, 0x74, 0x6f, 0x74, 0x61, 0x6c,
	0x4c, 0x6f, 0x61, 0x64, 0x1a, 0x38, 0x0a, 0x0a, 0x48, 0x6f, 0x73, 0x74, 0x73, 0x45, 0x6e, 0x74,
	0x72, 0x79, 0x12, 0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x04, 0x52,
	0x03, 0x6b, 0x65, 0x79, 0x12, 0x14, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x09, 0x52, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x1a, 0x59,
	0x0a, 0x0c, 0x4c, 0x6f, 0x61, 0x64, 0x4d, 0x61, 0x70, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12, 0x10,
	0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65, 0x79,
	0x12, 0x33, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x1d, 0x2e, 0x64, 0x61, 0x70, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61,
	0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x2e, 0x76, 0x31, 0x2e, 0x48, 0x6f, 0x73, 0x74, 0x52, 0x05,
	0x76, 0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x6e, 0x0a, 0x04, 0x48, 0x6f, 0x73,
	0x74, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52,
	0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x18, 0x02, 0x20,
	0x01, 0x28, 0x03, 0x52, 0x04, 0x70, 0x6f, 0x72, 0x74, 0x12, 0x12, 0x0a, 0x04, 0x6c, 0x6f, 0x61,
	0x64, 0x18, 0x03, 0x20, 0x01, 0x28, 0x03, 0x52, 0x04, 0x6c, 0x6f, 0x61, 0x64, 0x12, 0x1a, 0x0a,
	0x08, 0x65, 0x6e, 0x74, 0x69, 0x74, 0x69, 0x65, 0x73, 0x18, 0x04, 0x20, 0x03, 0x28, 0x09, 0x52,
	0x08, 0x65, 0x6e, 0x74, 0x69, 0x74, 0x69, 0x65, 0x73, 0x12, 0x0e, 0x0a, 0x02, 0x69, 0x64, 0x18,
	0x05, 0x20, 0x01, 0x28, 0x09, 0x52, 0x02, 0x69, 0x64, 0x32, 0x6d, 0x0a, 0x09, 0x50, 0x6c, 0x61,
	0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x12, 0x60, 0x0a, 0x10, 0x52, 0x65, 0x70, 0x6f, 0x72, 0x74,
	0x44, 0x61, 0x70, 0x72, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x1d, 0x2e, 0x64, 0x61, 0x70,
	0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e,
	0x74, 0x2e, 0x76, 0x31, 0x2e, 0x48, 0x6f, 0x73, 0x74, 0x1a, 0x27, 0x2e, 0x64, 0x61, 0x70, 0x72,
	0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2e, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74,
	0x2e, 0x76, 0x31, 0x2e, 0x50, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e, 0x74, 0x4f, 0x72, 0x64,
	0x65, 0x72, 0x22, 0x00, 0x28, 0x01, 0x30, 0x01, 0x42, 0x37, 0x5a, 0x35, 0x67, 0x69, 0x74, 0x68,
	0x75, 0x62, 0x2e, 0x63, 0x6f, 0x6d, 0x2f, 0x64, 0x61, 0x70, 0x72, 0x2f, 0x64, 0x61, 0x70, 0x72,
	0x2f, 0x70, 0x6b, 0x67, 0x2f, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x2f, 0x70, 0x6c, 0x61, 0x63, 0x65,
	0x6d, 0x65, 0x6e, 0x74, 0x2f, 0x76, 0x31, 0x3b, 0x70, 0x6c, 0x61, 0x63, 0x65, 0x6d, 0x65, 0x6e,
	0x74, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_dapr_proto_placement_v1_placement_proto_rawDescOnce sync.Once
	file_dapr_proto_placement_v1_placement_proto_rawDescData = file_dapr_proto_placement_v1_placement_proto_rawDesc
)

func file_dapr_proto_placement_v1_placement_proto_rawDescGZIP() []byte {
	file_dapr_proto_placement_v1_placement_proto_rawDescOnce.Do(func() {
		file_dapr_proto_placement_v1_placement_proto_rawDescData = protoimpl.X.CompressGZIP(file_dapr_proto_placement_v1_placement_proto_rawDescData)
	})
	return file_dapr_proto_placement_v1_placement_proto_rawDescData
}

var file_dapr_proto_placement_v1_placement_proto_msgTypes = make([]protoimpl.MessageInfo, 7)
var file_dapr_proto_placement_v1_placement_proto_goTypes = []interface{}{
	(*PlacementOrder)(nil),  // 0: dapr.proto.placement.v1.PlacementOrder
	(*PlacementTables)(nil), // 1: dapr.proto.placement.v1.PlacementTables
	(*PlacementTable)(nil),  // 2: dapr.proto.placement.v1.PlacementTable
	(*Host)(nil),            // 3: dapr.proto.placement.v1.Host
	nil,                     // 4: dapr.proto.placement.v1.PlacementTables.EntriesEntry
	nil,                     // 5: dapr.proto.placement.v1.PlacementTable.HostsEntry
	nil,                     // 6: dapr.proto.placement.v1.PlacementTable.LoadMapEntry
}
var file_dapr_proto_placement_v1_placement_proto_depIdxs = []int32{
	1, // 0: dapr.proto.placement.v1.PlacementOrder.tables:type_name -> dapr.proto.placement.v1.PlacementTables
	4, // 1: dapr.proto.placement.v1.PlacementTables.entries:type_name -> dapr.proto.placement.v1.PlacementTables.EntriesEntry
	5, // 2: dapr.proto.placement.v1.PlacementTable.hosts:type_name -> dapr.proto.placement.v1.PlacementTable.HostsEntry
	6, // 3: dapr.proto.placement.v1.PlacementTable.load_map:type_name -> dapr.proto.placement.v1.PlacementTable.LoadMapEntry
	2, // 4: dapr.proto.placement.v1.PlacementTables.EntriesEntry.value:type_name -> dapr.proto.placement.v1.PlacementTable
	3, // 5: dapr.proto.placement.v1.PlacementTable.LoadMapEntry.value:type_name -> dapr.proto.placement.v1.Host
	3, // 6: dapr.proto.placement.v1.Placement.ReportDaprStatus:input_type -> dapr.proto.placement.v1.Host
	0, // 7: dapr.proto.placement.v1.Placement.ReportDaprStatus:output_type -> dapr.proto.placement.v1.PlacementOrder
	7, // [7:8] is the sub-list for method output_type
	6, // [6:7] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_dapr_proto_placement_v1_placement_proto_init() }
func file_dapr_proto_placement_v1_placement_proto_init() {
	if File_dapr_proto_placement_v1_placement_proto != nil {
		return
	}
	if !protoimpl.UnsafeEnabled {
		file_dapr_proto_placement_v1_placement_proto_msgTypes[0].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PlacementOrder); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_placement_v1_placement_proto_msgTypes[1].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PlacementTables); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_placement_v1_placement_proto_msgTypes[2].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*PlacementTable); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
		file_dapr_proto_placement_v1_placement_proto_msgTypes[3].Exporter = func(v interface{}, i int) interface{} {
			switch v := v.(*Host); i {
			case 0:
				return &v.state
			case 1:
				return &v.sizeCache
			case 2:
				return &v.unknownFields
			default:
				return nil
			}
		}
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_dapr_proto_placement_v1_placement_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   7,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_dapr_proto_placement_v1_placement_proto_goTypes,
		DependencyIndexes: file_dapr_proto_placement_v1_placement_proto_depIdxs,
		MessageInfos:      file_dapr_proto_placement_v1_placement_proto_msgTypes,
	}.Build()
	File_dapr_proto_placement_v1_placement_proto = out.File
	file_dapr_proto_placement_v1_placement_proto_rawDesc = nil
	file_dapr_proto_placement_v1_placement_proto_goTypes = nil
	file_dapr_proto_placement_v1_placement_proto_depIdxs = nil
}
