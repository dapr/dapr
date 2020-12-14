// Code generated by protoc-gen-go. DO NOT EDIT.
// source: dapr/proto/runtime/v1/appcallback.proto

package runtime

import (
	context "context"
	fmt "fmt"
	v1 "github.com/dapr/dapr/pkg/proto/common/v1"
	proto "github.com/golang/protobuf/proto"
	empty "github.com/golang/protobuf/ptypes/empty"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
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

// TopicEventResponseStatus allows apps to have finer control over handling of the message.
type TopicEventResponse_TopicEventResponseStatus int32

const (
	// SUCCESS is the default behavior: message is acknowledged and not retried or logged.
	TopicEventResponse_SUCCESS TopicEventResponse_TopicEventResponseStatus = 0
	// RETRY status signals Dapr to retry the message as part of an expected scenario (no warning is logged).
	TopicEventResponse_RETRY TopicEventResponse_TopicEventResponseStatus = 1
	// DROP status signals Dapr to drop the message as part of an unexpected scenario (warning is logged).
	TopicEventResponse_DROP TopicEventResponse_TopicEventResponseStatus = 2
)

var TopicEventResponse_TopicEventResponseStatus_name = map[int32]string{
	0: "SUCCESS",
	1: "RETRY",
	2: "DROP",
}

var TopicEventResponse_TopicEventResponseStatus_value = map[string]int32{
	"SUCCESS": 0,
	"RETRY":   1,
	"DROP":    2,
}

func (x TopicEventResponse_TopicEventResponseStatus) String() string {
	return proto.EnumName(TopicEventResponse_TopicEventResponseStatus_name, int32(x))
}

func (TopicEventResponse_TopicEventResponseStatus) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{1, 0}
}

// BindingEventConcurrency is the kind of concurrency
type BindingEventResponse_BindingEventConcurrency int32

const (
	// SEQUENTIAL sends data to output bindings specified in "to" sequentially.
	BindingEventResponse_SEQUENTIAL BindingEventResponse_BindingEventConcurrency = 0
	// PARALLEL sends data to output bindings specified in "to" in parallel.
	BindingEventResponse_PARALLEL BindingEventResponse_BindingEventConcurrency = 1
)

var BindingEventResponse_BindingEventConcurrency_name = map[int32]string{
	0: "SEQUENTIAL",
	1: "PARALLEL",
}

var BindingEventResponse_BindingEventConcurrency_value = map[string]int32{
	"SEQUENTIAL": 0,
	"PARALLEL":   1,
}

func (x BindingEventResponse_BindingEventConcurrency) String() string {
	return proto.EnumName(BindingEventResponse_BindingEventConcurrency_name, int32(x))
}

func (BindingEventResponse_BindingEventConcurrency) EnumDescriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{3, 0}
}

// TopicEventRequest message is compatible with CloudEvent spec v1.0
// https://github.com/cloudevents/spec/blob/v1.0/spec.md
type TopicEventRequest struct {
	// id identifies the event. Producers MUST ensure that source + id
	// is unique for each distinct event. If a duplicate event is re-sent
	// (e.g. due to a network error) it MAY have the same id.
	Id string `protobuf:"bytes,1,opt,name=id,proto3" json:"id,omitempty"`
	// source identifies the context in which an event happened.
	// Often this will include information such as the type of the
	// event source, the organization publishing the event or the process
	// that produced the event. The exact syntax and semantics behind
	// the data encoded in the URI is defined by the event producer.
	Source string `protobuf:"bytes,2,opt,name=source,proto3" json:"source,omitempty"`
	// The type of event related to the originating occurrence.
	Type string `protobuf:"bytes,3,opt,name=type,proto3" json:"type,omitempty"`
	// The version of the CloudEvents specification.
	SpecVersion string `protobuf:"bytes,4,opt,name=spec_version,json=specVersion,proto3" json:"spec_version,omitempty"`
	// The content type of data value.
	DataContentType string `protobuf:"bytes,5,opt,name=data_content_type,json=dataContentType,proto3" json:"data_content_type,omitempty"`
	// The content of the event.
	Data []byte `protobuf:"bytes,7,opt,name=data,proto3" json:"data,omitempty"`
	// The pubsub topic which publisher sent to.
	Topic string `protobuf:"bytes,6,opt,name=topic,proto3" json:"topic,omitempty"`
	// The name of the pubsub the publisher sent to.
	PubsubName           string   `protobuf:"bytes,8,opt,name=pubsub_name,json=pubsubName,proto3" json:"pubsub_name,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *TopicEventRequest) Reset()         { *m = TopicEventRequest{} }
func (m *TopicEventRequest) String() string { return proto.CompactTextString(m) }
func (*TopicEventRequest) ProtoMessage()    {}
func (*TopicEventRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{0}
}

func (m *TopicEventRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TopicEventRequest.Unmarshal(m, b)
}
func (m *TopicEventRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TopicEventRequest.Marshal(b, m, deterministic)
}
func (m *TopicEventRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TopicEventRequest.Merge(m, src)
}
func (m *TopicEventRequest) XXX_Size() int {
	return xxx_messageInfo_TopicEventRequest.Size(m)
}
func (m *TopicEventRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_TopicEventRequest.DiscardUnknown(m)
}

var xxx_messageInfo_TopicEventRequest proto.InternalMessageInfo

func (m *TopicEventRequest) GetId() string {
	if m != nil {
		return m.Id
	}
	return ""
}

func (m *TopicEventRequest) GetSource() string {
	if m != nil {
		return m.Source
	}
	return ""
}

func (m *TopicEventRequest) GetType() string {
	if m != nil {
		return m.Type
	}
	return ""
}

func (m *TopicEventRequest) GetSpecVersion() string {
	if m != nil {
		return m.SpecVersion
	}
	return ""
}

func (m *TopicEventRequest) GetDataContentType() string {
	if m != nil {
		return m.DataContentType
	}
	return ""
}

func (m *TopicEventRequest) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func (m *TopicEventRequest) GetTopic() string {
	if m != nil {
		return m.Topic
	}
	return ""
}

func (m *TopicEventRequest) GetPubsubName() string {
	if m != nil {
		return m.PubsubName
	}
	return ""
}

// TopicEventResponse is response from app on published message
type TopicEventResponse struct {
	// The list of output bindings.
	Status               TopicEventResponse_TopicEventResponseStatus `protobuf:"varint,1,opt,name=status,proto3,enum=dapr.proto.runtime.v1.TopicEventResponse_TopicEventResponseStatus" json:"status,omitempty"`
	XXX_NoUnkeyedLiteral struct{}                                    `json:"-"`
	XXX_unrecognized     []byte                                      `json:"-"`
	XXX_sizecache        int32                                       `json:"-"`
}

func (m *TopicEventResponse) Reset()         { *m = TopicEventResponse{} }
func (m *TopicEventResponse) String() string { return proto.CompactTextString(m) }
func (*TopicEventResponse) ProtoMessage()    {}
func (*TopicEventResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{1}
}

func (m *TopicEventResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TopicEventResponse.Unmarshal(m, b)
}
func (m *TopicEventResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TopicEventResponse.Marshal(b, m, deterministic)
}
func (m *TopicEventResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TopicEventResponse.Merge(m, src)
}
func (m *TopicEventResponse) XXX_Size() int {
	return xxx_messageInfo_TopicEventResponse.Size(m)
}
func (m *TopicEventResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_TopicEventResponse.DiscardUnknown(m)
}

var xxx_messageInfo_TopicEventResponse proto.InternalMessageInfo

func (m *TopicEventResponse) GetStatus() TopicEventResponse_TopicEventResponseStatus {
	if m != nil {
		return m.Status
	}
	return TopicEventResponse_SUCCESS
}

// BindingEventRequest represents input bindings event.
type BindingEventRequest struct {
	// Required. The name of the input binding component.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Required. The payload that the input bindings sent
	Data []byte `protobuf:"bytes,2,opt,name=data,proto3" json:"data,omitempty"`
	// The metadata set by the input binging components.
	Metadata             map[string]string `protobuf:"bytes,3,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	XXX_NoUnkeyedLiteral struct{}          `json:"-"`
	XXX_unrecognized     []byte            `json:"-"`
	XXX_sizecache        int32             `json:"-"`
}

func (m *BindingEventRequest) Reset()         { *m = BindingEventRequest{} }
func (m *BindingEventRequest) String() string { return proto.CompactTextString(m) }
func (*BindingEventRequest) ProtoMessage()    {}
func (*BindingEventRequest) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{2}
}

func (m *BindingEventRequest) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_BindingEventRequest.Unmarshal(m, b)
}
func (m *BindingEventRequest) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_BindingEventRequest.Marshal(b, m, deterministic)
}
func (m *BindingEventRequest) XXX_Merge(src proto.Message) {
	xxx_messageInfo_BindingEventRequest.Merge(m, src)
}
func (m *BindingEventRequest) XXX_Size() int {
	return xxx_messageInfo_BindingEventRequest.Size(m)
}
func (m *BindingEventRequest) XXX_DiscardUnknown() {
	xxx_messageInfo_BindingEventRequest.DiscardUnknown(m)
}

var xxx_messageInfo_BindingEventRequest proto.InternalMessageInfo

func (m *BindingEventRequest) GetName() string {
	if m != nil {
		return m.Name
	}
	return ""
}

func (m *BindingEventRequest) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func (m *BindingEventRequest) GetMetadata() map[string]string {
	if m != nil {
		return m.Metadata
	}
	return nil
}

// BindingEventResponse includes operations to save state or
// send data to output bindings optionally.
type BindingEventResponse struct {
	// The name of state store where states are saved.
	StoreName string `protobuf:"bytes,1,opt,name=store_name,json=storeName,proto3" json:"store_name,omitempty"`
	// The state key values which will be stored in store_name.
	States []*v1.StateItem `protobuf:"bytes,2,rep,name=states,proto3" json:"states,omitempty"`
	// The list of output bindings.
	To []string `protobuf:"bytes,3,rep,name=to,proto3" json:"to,omitempty"`
	// The content which will be sent to "to" output bindings.
	Data []byte `protobuf:"bytes,4,opt,name=data,proto3" json:"data,omitempty"`
	// The concurrency of output bindings to send data to
	// "to" output bindings list. The default is SEQUENTIAL.
	Concurrency          BindingEventResponse_BindingEventConcurrency `protobuf:"varint,5,opt,name=concurrency,proto3,enum=dapr.proto.runtime.v1.BindingEventResponse_BindingEventConcurrency" json:"concurrency,omitempty"`
	XXX_NoUnkeyedLiteral struct{}                                     `json:"-"`
	XXX_unrecognized     []byte                                       `json:"-"`
	XXX_sizecache        int32                                        `json:"-"`
}

func (m *BindingEventResponse) Reset()         { *m = BindingEventResponse{} }
func (m *BindingEventResponse) String() string { return proto.CompactTextString(m) }
func (*BindingEventResponse) ProtoMessage()    {}
func (*BindingEventResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{3}
}

func (m *BindingEventResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_BindingEventResponse.Unmarshal(m, b)
}
func (m *BindingEventResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_BindingEventResponse.Marshal(b, m, deterministic)
}
func (m *BindingEventResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_BindingEventResponse.Merge(m, src)
}
func (m *BindingEventResponse) XXX_Size() int {
	return xxx_messageInfo_BindingEventResponse.Size(m)
}
func (m *BindingEventResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_BindingEventResponse.DiscardUnknown(m)
}

var xxx_messageInfo_BindingEventResponse proto.InternalMessageInfo

func (m *BindingEventResponse) GetStoreName() string {
	if m != nil {
		return m.StoreName
	}
	return ""
}

func (m *BindingEventResponse) GetStates() []*v1.StateItem {
	if m != nil {
		return m.States
	}
	return nil
}

func (m *BindingEventResponse) GetTo() []string {
	if m != nil {
		return m.To
	}
	return nil
}

func (m *BindingEventResponse) GetData() []byte {
	if m != nil {
		return m.Data
	}
	return nil
}

func (m *BindingEventResponse) GetConcurrency() BindingEventResponse_BindingEventConcurrency {
	if m != nil {
		return m.Concurrency
	}
	return BindingEventResponse_SEQUENTIAL
}

// ListTopicSubscriptionsResponse is the message including the list of the subscribing topics.
type ListTopicSubscriptionsResponse struct {
	// The list of topics.
	Subscriptions        []*TopicSubscription `protobuf:"bytes,1,rep,name=subscriptions,proto3" json:"subscriptions,omitempty"`
	XXX_NoUnkeyedLiteral struct{}             `json:"-"`
	XXX_unrecognized     []byte               `json:"-"`
	XXX_sizecache        int32                `json:"-"`
}

func (m *ListTopicSubscriptionsResponse) Reset()         { *m = ListTopicSubscriptionsResponse{} }
func (m *ListTopicSubscriptionsResponse) String() string { return proto.CompactTextString(m) }
func (*ListTopicSubscriptionsResponse) ProtoMessage()    {}
func (*ListTopicSubscriptionsResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{4}
}

func (m *ListTopicSubscriptionsResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ListTopicSubscriptionsResponse.Unmarshal(m, b)
}
func (m *ListTopicSubscriptionsResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ListTopicSubscriptionsResponse.Marshal(b, m, deterministic)
}
func (m *ListTopicSubscriptionsResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ListTopicSubscriptionsResponse.Merge(m, src)
}
func (m *ListTopicSubscriptionsResponse) XXX_Size() int {
	return xxx_messageInfo_ListTopicSubscriptionsResponse.Size(m)
}
func (m *ListTopicSubscriptionsResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_ListTopicSubscriptionsResponse.DiscardUnknown(m)
}

var xxx_messageInfo_ListTopicSubscriptionsResponse proto.InternalMessageInfo

func (m *ListTopicSubscriptionsResponse) GetSubscriptions() []*TopicSubscription {
	if m != nil {
		return m.Subscriptions
	}
	return nil
}

// TopicSubscription represents topic and metadata.
type TopicSubscription struct {
	// Required. The name of the pubsub containing the topic below to subscribe to.
	PubsubName string `protobuf:"bytes,1,opt,name=pubsub_name,json=pubsubName,proto3" json:"pubsub_name,omitempty"`
	// Required. The name of topic which will be subscribed
	Topic string `protobuf:"bytes,2,opt,name=topic,proto3" json:"topic,omitempty"`
	// The optional properties used for this topic's subscription e.g. session id
	Metadata             map[string]string `protobuf:"bytes,3,rep,name=metadata,proto3" json:"metadata,omitempty" protobuf_key:"bytes,1,opt,name=key,proto3" protobuf_val:"bytes,2,opt,name=value,proto3"`
	XXX_NoUnkeyedLiteral struct{}          `json:"-"`
	XXX_unrecognized     []byte            `json:"-"`
	XXX_sizecache        int32             `json:"-"`
}

func (m *TopicSubscription) Reset()         { *m = TopicSubscription{} }
func (m *TopicSubscription) String() string { return proto.CompactTextString(m) }
func (*TopicSubscription) ProtoMessage()    {}
func (*TopicSubscription) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{5}
}

func (m *TopicSubscription) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_TopicSubscription.Unmarshal(m, b)
}
func (m *TopicSubscription) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_TopicSubscription.Marshal(b, m, deterministic)
}
func (m *TopicSubscription) XXX_Merge(src proto.Message) {
	xxx_messageInfo_TopicSubscription.Merge(m, src)
}
func (m *TopicSubscription) XXX_Size() int {
	return xxx_messageInfo_TopicSubscription.Size(m)
}
func (m *TopicSubscription) XXX_DiscardUnknown() {
	xxx_messageInfo_TopicSubscription.DiscardUnknown(m)
}

var xxx_messageInfo_TopicSubscription proto.InternalMessageInfo

func (m *TopicSubscription) GetPubsubName() string {
	if m != nil {
		return m.PubsubName
	}
	return ""
}

func (m *TopicSubscription) GetTopic() string {
	if m != nil {
		return m.Topic
	}
	return ""
}

func (m *TopicSubscription) GetMetadata() map[string]string {
	if m != nil {
		return m.Metadata
	}
	return nil
}

// ListInputBindingsResponse is the message including the list of input bindings.
type ListInputBindingsResponse struct {
	// The list of input bindings.
	Bindings             []string `protobuf:"bytes,1,rep,name=bindings,proto3" json:"bindings,omitempty"`
	XXX_NoUnkeyedLiteral struct{} `json:"-"`
	XXX_unrecognized     []byte   `json:"-"`
	XXX_sizecache        int32    `json:"-"`
}

func (m *ListInputBindingsResponse) Reset()         { *m = ListInputBindingsResponse{} }
func (m *ListInputBindingsResponse) String() string { return proto.CompactTextString(m) }
func (*ListInputBindingsResponse) ProtoMessage()    {}
func (*ListInputBindingsResponse) Descriptor() ([]byte, []int) {
	return fileDescriptor_830251cb323c018d, []int{6}
}

func (m *ListInputBindingsResponse) XXX_Unmarshal(b []byte) error {
	return xxx_messageInfo_ListInputBindingsResponse.Unmarshal(m, b)
}
func (m *ListInputBindingsResponse) XXX_Marshal(b []byte, deterministic bool) ([]byte, error) {
	return xxx_messageInfo_ListInputBindingsResponse.Marshal(b, m, deterministic)
}
func (m *ListInputBindingsResponse) XXX_Merge(src proto.Message) {
	xxx_messageInfo_ListInputBindingsResponse.Merge(m, src)
}
func (m *ListInputBindingsResponse) XXX_Size() int {
	return xxx_messageInfo_ListInputBindingsResponse.Size(m)
}
func (m *ListInputBindingsResponse) XXX_DiscardUnknown() {
	xxx_messageInfo_ListInputBindingsResponse.DiscardUnknown(m)
}

var xxx_messageInfo_ListInputBindingsResponse proto.InternalMessageInfo

func (m *ListInputBindingsResponse) GetBindings() []string {
	if m != nil {
		return m.Bindings
	}
	return nil
}

func init() {
	proto.RegisterEnum("dapr.proto.runtime.v1.TopicEventResponse_TopicEventResponseStatus", TopicEventResponse_TopicEventResponseStatus_name, TopicEventResponse_TopicEventResponseStatus_value)
	proto.RegisterEnum("dapr.proto.runtime.v1.BindingEventResponse_BindingEventConcurrency", BindingEventResponse_BindingEventConcurrency_name, BindingEventResponse_BindingEventConcurrency_value)
	proto.RegisterType((*TopicEventRequest)(nil), "dapr.proto.runtime.v1.TopicEventRequest")
	proto.RegisterType((*TopicEventResponse)(nil), "dapr.proto.runtime.v1.TopicEventResponse")
	proto.RegisterType((*BindingEventRequest)(nil), "dapr.proto.runtime.v1.BindingEventRequest")
	proto.RegisterMapType((map[string]string)(nil), "dapr.proto.runtime.v1.BindingEventRequest.MetadataEntry")
	proto.RegisterType((*BindingEventResponse)(nil), "dapr.proto.runtime.v1.BindingEventResponse")
	proto.RegisterType((*ListTopicSubscriptionsResponse)(nil), "dapr.proto.runtime.v1.ListTopicSubscriptionsResponse")
	proto.RegisterType((*TopicSubscription)(nil), "dapr.proto.runtime.v1.TopicSubscription")
	proto.RegisterMapType((map[string]string)(nil), "dapr.proto.runtime.v1.TopicSubscription.MetadataEntry")
	proto.RegisterType((*ListInputBindingsResponse)(nil), "dapr.proto.runtime.v1.ListInputBindingsResponse")
}

func init() {
	proto.RegisterFile("dapr/proto/runtime/v1/appcallback.proto", fileDescriptor_830251cb323c018d)
}

var fileDescriptor_830251cb323c018d = []byte{
	// 838 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x09, 0x6e, 0x88, 0x02, 0xff, 0xb4, 0x55, 0xdd, 0x92, 0xe2, 0x44,
	0x14, 0x9e, 0x64, 0x98, 0x59, 0xe6, 0x30, 0x8b, 0x4c, 0xbb, 0x3b, 0x46, 0x2c, 0xdd, 0xd9, 0x68,
	0x95, 0xb8, 0x56, 0x05, 0xc1, 0xd2, 0xd9, 0x72, 0xbd, 0x01, 0x36, 0x65, 0x51, 0x85, 0xc3, 0x18,
	0x58, 0x2d, 0xf7, 0x86, 0x0a, 0xa1, 0xc5, 0x14, 0xd0, 0xdd, 0x26, 0x1d, 0xaa, 0x78, 0x01, 0x1f,
	0xc6, 0x0b, 0x2f, 0x7c, 0x12, 0xef, 0x7d, 0x02, 0xdf, 0xc2, 0xfe, 0x09, 0x10, 0x96, 0x9f, 0xc2,
	0x8b, 0xbd, 0x3b, 0x39, 0x7d, 0xfa, 0x3b, 0x7d, 0xbe, 0xf3, 0x9d, 0x13, 0xf8, 0x74, 0xe4, 0xb3,
	0xa8, 0xca, 0x22, 0xca, 0x69, 0x35, 0x4a, 0x08, 0x0f, 0x67, 0xb8, 0x3a, 0xaf, 0x55, 0x7d, 0xc6,
	0x02, 0x7f, 0x3a, 0x1d, 0xfa, 0xc1, 0xc4, 0x51, 0x87, 0xe8, 0xb1, 0x0c, 0xd4, 0xb6, 0x93, 0x06,
	0x3a, 0xf3, 0x5a, 0xf9, 0x83, 0x31, 0xa5, 0xe3, 0x29, 0xd6, 0x08, 0xc3, 0xe4, 0x97, 0x2a, 0x9e,
	0x31, 0xbe, 0xd0, 0x71, 0xe5, 0xa7, 0x19, 0xf0, 0x80, 0xce, 0x66, 0x94, 0x48, 0x6c, 0x6d, 0xe9,
	0x10, 0xfb, 0x5f, 0x03, 0xae, 0xfa, 0x94, 0x85, 0x81, 0x3b, 0xc7, 0x84, 0x7b, 0xf8, 0xb7, 0x04,
	0xc7, 0x1c, 0x15, 0xc1, 0x0c, 0x47, 0x96, 0x71, 0x63, 0x54, 0x2e, 0x3c, 0x61, 0xa1, 0x6b, 0x38,
	0x8f, 0x69, 0x12, 0x05, 0xd8, 0x32, 0x95, 0x2f, 0xfd, 0x42, 0x08, 0x72, 0x7c, 0xc1, 0xb0, 0x75,
	0xaa, 0xbc, 0xca, 0x46, 0x4f, 0xe1, 0x32, 0x66, 0x38, 0x18, 0xcc, 0x71, 0x14, 0x87, 0x94, 0x58,
	0x39, 0x75, 0x56, 0x90, 0xbe, 0x1f, 0xb5, 0x0b, 0x3d, 0x83, 0xab, 0x91, 0xcf, 0xfd, 0x41, 0x40,
	0x09, 0x17, 0x59, 0x07, 0x0a, 0xe3, 0x4c, 0xc5, 0xbd, 0x23, 0x0f, 0x5a, 0xda, 0xdf, 0x97, 0x70,
	0x22, 0x85, 0x74, 0x59, 0x0f, 0xc4, 0xf1, 0xa5, 0xa7, 0x6c, 0xf4, 0x08, 0xce, 0xb8, 0x7c, 0xb3,
	0x75, 0xae, 0xee, 0xe8, 0x0f, 0xf4, 0x04, 0x0a, 0x2c, 0x19, 0xc6, 0xc9, 0x70, 0x40, 0xfc, 0x19,
	0xb6, 0xf2, 0xea, 0x0c, 0xb4, 0xeb, 0x4e, 0x78, 0xec, 0x3f, 0x0d, 0x40, 0xd9, 0x5a, 0x63, 0x46,
	0x49, 0x8c, 0xd1, 0x6b, 0x51, 0x1c, 0xf7, 0x79, 0x12, 0xab, 0x82, 0x8b, 0xf5, 0xa6, 0xb3, 0x93,
	0x6a, 0x67, 0xfb, 0xea, 0x0e, 0x57, 0x4f, 0x21, 0x79, 0x29, 0xa2, 0xfd, 0x2d, 0x58, 0xfb, 0x62,
	0x50, 0x01, 0x1e, 0xf4, 0x5e, 0xb5, 0x5a, 0x6e, 0xaf, 0x57, 0x3a, 0x41, 0x17, 0x70, 0xe6, 0xb9,
	0x7d, 0xef, 0xe7, 0x92, 0x81, 0xf2, 0x90, 0x7b, 0xe9, 0x75, 0xef, 0x4b, 0xa6, 0xfd, 0xb7, 0x01,
	0xef, 0x36, 0x43, 0x32, 0x0a, 0xc9, 0x78, 0xa3, 0x3d, 0x82, 0x13, 0x55, 0xa2, 0x6e, 0x90, 0xb2,
	0x57, 0x3c, 0x99, 0x19, 0x9e, 0xfa, 0x90, 0x9f, 0x61, 0xee, 0x2b, 0xff, 0xe9, 0xcd, 0x69, 0xa5,
	0x50, 0x7f, 0xbe, 0xa7, 0xb6, 0x1d, 0x59, 0x9c, 0xef, 0xd3, 0xab, 0x2e, 0xe1, 0xd1, 0xc2, 0x5b,
	0x21, 0x95, 0x5f, 0xc0, 0xc3, 0x8d, 0x23, 0x54, 0x82, 0xd3, 0x09, 0x5e, 0xa4, 0xaf, 0x91, 0xa6,
	0x6c, 0xd0, 0xdc, 0x9f, 0x26, 0x4b, 0xb9, 0xe8, 0x8f, 0x6f, 0xcc, 0xe7, 0x86, 0xfd, 0x97, 0x09,
	0x8f, 0x36, 0x93, 0xa5, 0x5d, 0xf8, 0x10, 0x20, 0xe6, 0x34, 0xc2, 0x83, 0x4c, 0x65, 0x17, 0xca,
	0x23, 0x7b, 0x87, 0x6e, 0x75, 0x93, 0x70, 0x2c, 0x20, 0x65, 0x21, 0x4f, 0xb2, 0x85, 0xa4, 0x8a,
	0x16, 0x75, 0x48, 0x6a, 0x71, 0x9b, 0xe3, 0x99, 0x97, 0x86, 0x4b, 0x29, 0x73, 0xaa, 0xaa, 0x17,
	0x52, 0x16, 0x73, 0xb4, 0xe4, 0x29, 0x97, 0xe1, 0x09, 0x43, 0x41, 0x48, 0x31, 0x48, 0xa2, 0x08,
	0x93, 0x60, 0xa1, 0x94, 0x58, 0xac, 0xb7, 0x8e, 0xa2, 0x2a, 0x15, 0x42, 0xd6, 0xd9, 0x5a, 0x43,
	0x79, 0x59, 0x5c, 0xfb, 0x16, 0xde, 0xdb, 0x13, 0x27, 0x5e, 0x09, 0x3d, 0xf7, 0x87, 0x57, 0xee,
	0x5d, 0xbf, 0xdd, 0xe8, 0x08, 0x39, 0x5c, 0x42, 0xfe, 0xbe, 0xe1, 0x35, 0x3a, 0x1d, 0xb7, 0x53,
	0x32, 0x6c, 0x06, 0x1f, 0x75, 0xc2, 0x98, 0x2b, 0x25, 0xf5, 0x84, 0x9e, 0x83, 0x28, 0x64, 0x5c,
	0x0c, 0x52, 0xbc, 0x62, 0xef, 0x0e, 0x1e, 0xc6, 0xd9, 0x03, 0x41, 0xa0, 0x64, 0xa9, 0x72, 0x48,
	0xca, 0x59, 0x24, 0x6f, 0xf3, 0xba, 0xfd, 0xcf, 0x72, 0x2d, 0x64, 0x83, 0xde, 0x9c, 0x30, 0xe3,
	0xcd, 0x09, 0x5b, 0x0f, 0xa6, 0x99, 0x1d, 0x4c, 0x6f, 0x4b, 0x86, 0x5f, 0x1f, 0xfb, 0xae, 0xb7,
	0x23, 0xc2, 0x5b, 0x78, 0x5f, 0xf2, 0xd9, 0x26, 0x2c, 0xe1, 0x69, 0x47, 0xd6, 0x54, 0x96, 0x21,
	0x3f, 0x4c, 0x7d, 0x8a, 0xc5, 0x0b, 0x6f, 0xf5, 0x5d, 0xff, 0x3d, 0x07, 0x85, 0x06, 0x63, 0xad,
	0x74, 0x35, 0xa3, 0x9f, 0x20, 0xdf, 0x25, 0x6d, 0x32, 0xa7, 0x13, 0x8c, 0x3e, 0xde, 0xad, 0x48,
	0x7d, 0x9a, 0xce, 0x54, 0xf9, 0x93, 0xc3, 0x41, 0xfa, 0x09, 0xf6, 0x09, 0x0a, 0xe1, 0x7a, 0x77,
	0xc7, 0xd1, 0xb5, 0xa3, 0x37, 0xbe, 0xb3, 0xdc, 0xf8, 0x8e, 0x2b, 0x37, 0x7e, 0xf9, 0xab, 0x3d,
	0x94, 0x1e, 0x16, 0x8e, 0x48, 0x85, 0xe1, 0xb2, 0x4b, 0xd6, 0x4b, 0x0a, 0x55, 0x8e, 0x58, 0x7f,
	0xba, 0x98, 0xcf, 0x8e, 0x5e, 0x94, 0x22, 0xcd, 0x00, 0xae, 0xb6, 0x38, 0xdf, 0x5b, 0xcc, 0x17,
	0x07, 0x8a, 0xd9, 0xd9, 0x35, 0x91, 0x60, 0x02, 0xc5, 0x2e, 0xc9, 0xce, 0x17, 0x7a, 0x76, 0xfc,
	0xb2, 0x2b, 0x7f, 0xfe, 0x3f, 0xa6, 0xdd, 0x3e, 0x69, 0x2e, 0x00, 0x42, 0xaa, 0xaf, 0xcc, 0x6b,
	0xcd, 0xc7, 0x2f, 0x85, 0x91, 0xd1, 0xc5, 0xbd, 0x44, 0x89, 0x5f, 0xd7, 0xc6, 0x21, 0xff, 0x35,
	0x19, 0xca, 0x3e, 0x57, 0xd5, 0x9f, 0x58, 0xff, 0x8e, 0x27, 0xe3, 0xad, 0xff, 0xfd, 0x8b, 0xd4,
	0xfc, 0xc3, 0xbc, 0x91, 0x50, 0x4e, 0x06, 0xcb, 0x69, 0x24, 0x9c, 0x8e, 0x31, 0x71, 0xbe, 0x8b,
	0x58, 0x20, 0x92, 0x0d, 0xcf, 0xd5, 0xe5, 0x2f, 0xff, 0x0b, 0x00, 0x00, 0xff, 0xff, 0x63, 0x88,
	0x4c, 0x21, 0x3a, 0x08, 0x00, 0x00,
}

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConnInterface

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion6

// AppCallbackClient is the client API for AppCallback service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type AppCallbackClient interface {
	// Invokes service method with InvokeRequest.
	OnInvoke(ctx context.Context, in *v1.InvokeRequest, opts ...grpc.CallOption) (*v1.InvokeResponse, error)
	// Lists all topics subscribed by this app.
	ListTopicSubscriptions(ctx context.Context, in *empty.Empty, opts ...grpc.CallOption) (*ListTopicSubscriptionsResponse, error)
	// Subscribes events from Pubsub
	OnTopicEvent(ctx context.Context, in *TopicEventRequest, opts ...grpc.CallOption) (*TopicEventResponse, error)
	// Lists all input bindings subscribed by this app.
	ListInputBindings(ctx context.Context, in *empty.Empty, opts ...grpc.CallOption) (*ListInputBindingsResponse, error)
	// Listens events from the input bindings
	//
	// User application can save the states or send the events to the output
	// bindings optionally by returning BindingEventResponse.
	OnBindingEvent(ctx context.Context, in *BindingEventRequest, opts ...grpc.CallOption) (*BindingEventResponse, error)
}

type appCallbackClient struct {
	cc grpc.ClientConnInterface
}

func NewAppCallbackClient(cc grpc.ClientConnInterface) AppCallbackClient {
	return &appCallbackClient{cc}
}

func (c *appCallbackClient) OnInvoke(ctx context.Context, in *v1.InvokeRequest, opts ...grpc.CallOption) (*v1.InvokeResponse, error) {
	out := new(v1.InvokeResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallback/OnInvoke", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *appCallbackClient) ListTopicSubscriptions(ctx context.Context, in *empty.Empty, opts ...grpc.CallOption) (*ListTopicSubscriptionsResponse, error) {
	out := new(ListTopicSubscriptionsResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallback/ListTopicSubscriptions", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *appCallbackClient) OnTopicEvent(ctx context.Context, in *TopicEventRequest, opts ...grpc.CallOption) (*TopicEventResponse, error) {
	out := new(TopicEventResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallback/OnTopicEvent", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *appCallbackClient) ListInputBindings(ctx context.Context, in *empty.Empty, opts ...grpc.CallOption) (*ListInputBindingsResponse, error) {
	out := new(ListInputBindingsResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallback/ListInputBindings", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *appCallbackClient) OnBindingEvent(ctx context.Context, in *BindingEventRequest, opts ...grpc.CallOption) (*BindingEventResponse, error) {
	out := new(BindingEventResponse)
	err := c.cc.Invoke(ctx, "/dapr.proto.runtime.v1.AppCallback/OnBindingEvent", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// AppCallbackServer is the server API for AppCallback service.
type AppCallbackServer interface {
	// Invokes service method with InvokeRequest.
	OnInvoke(context.Context, *v1.InvokeRequest) (*v1.InvokeResponse, error)
	// Lists all topics subscribed by this app.
	ListTopicSubscriptions(context.Context, *empty.Empty) (*ListTopicSubscriptionsResponse, error)
	// Subscribes events from Pubsub
	OnTopicEvent(context.Context, *TopicEventRequest) (*TopicEventResponse, error)
	// Lists all input bindings subscribed by this app.
	ListInputBindings(context.Context, *empty.Empty) (*ListInputBindingsResponse, error)
	// Listens events from the input bindings
	//
	// User application can save the states or send the events to the output
	// bindings optionally by returning BindingEventResponse.
	OnBindingEvent(context.Context, *BindingEventRequest) (*BindingEventResponse, error)
}

// UnimplementedAppCallbackServer can be embedded to have forward compatible implementations.
type UnimplementedAppCallbackServer struct {
}

func (*UnimplementedAppCallbackServer) OnInvoke(ctx context.Context, req *v1.InvokeRequest) (*v1.InvokeResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method OnInvoke not implemented")
}
func (*UnimplementedAppCallbackServer) ListTopicSubscriptions(ctx context.Context, req *empty.Empty) (*ListTopicSubscriptionsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListTopicSubscriptions not implemented")
}
func (*UnimplementedAppCallbackServer) OnTopicEvent(ctx context.Context, req *TopicEventRequest) (*TopicEventResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method OnTopicEvent not implemented")
}
func (*UnimplementedAppCallbackServer) ListInputBindings(ctx context.Context, req *empty.Empty) (*ListInputBindingsResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method ListInputBindings not implemented")
}
func (*UnimplementedAppCallbackServer) OnBindingEvent(ctx context.Context, req *BindingEventRequest) (*BindingEventResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method OnBindingEvent not implemented")
}

func RegisterAppCallbackServer(s *grpc.Server, srv AppCallbackServer) {
	s.RegisterService(&_AppCallback_serviceDesc, srv)
}

func _AppCallback_OnInvoke_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(v1.InvokeRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AppCallbackServer).OnInvoke(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.runtime.v1.AppCallback/OnInvoke",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AppCallbackServer).OnInvoke(ctx, req.(*v1.InvokeRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _AppCallback_ListTopicSubscriptions_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(empty.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AppCallbackServer).ListTopicSubscriptions(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.runtime.v1.AppCallback/ListTopicSubscriptions",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AppCallbackServer).ListTopicSubscriptions(ctx, req.(*empty.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _AppCallback_OnTopicEvent_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(TopicEventRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AppCallbackServer).OnTopicEvent(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.runtime.v1.AppCallback/OnTopicEvent",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AppCallbackServer).OnTopicEvent(ctx, req.(*TopicEventRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _AppCallback_ListInputBindings_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(empty.Empty)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AppCallbackServer).ListInputBindings(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.runtime.v1.AppCallback/ListInputBindings",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AppCallbackServer).ListInputBindings(ctx, req.(*empty.Empty))
	}
	return interceptor(ctx, in, info, handler)
}

func _AppCallback_OnBindingEvent_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(BindingEventRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(AppCallbackServer).OnBindingEvent(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: "/dapr.proto.runtime.v1.AppCallback/OnBindingEvent",
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(AppCallbackServer).OnBindingEvent(ctx, req.(*BindingEventRequest))
	}
	return interceptor(ctx, in, info, handler)
}

var _AppCallback_serviceDesc = grpc.ServiceDesc{
	ServiceName: "dapr.proto.runtime.v1.AppCallback",
	HandlerType: (*AppCallbackServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "OnInvoke",
			Handler:    _AppCallback_OnInvoke_Handler,
		},
		{
			MethodName: "ListTopicSubscriptions",
			Handler:    _AppCallback_ListTopicSubscriptions_Handler,
		},
		{
			MethodName: "OnTopicEvent",
			Handler:    _AppCallback_OnTopicEvent_Handler,
		},
		{
			MethodName: "ListInputBindings",
			Handler:    _AppCallback_ListInputBindings_Handler,
		},
		{
			MethodName: "OnBindingEvent",
			Handler:    _AppCallback_OnBindingEvent_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "dapr/proto/runtime/v1/appcallback.proto",
}
