package servicebus

//	MIT License
//
//	Copyright (c) Microsoft Corporation. All rights reserved.
//
//	Permission is hereby granted, free of charge, to any person obtaining a copy
//	of this software and associated documentation files (the "Software"), to deal
//	in the Software without restriction, including without limitation the rights
//	to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//	copies of the Software, and to permit persons to whom the Software is
//	furnished to do so, subject to the following conditions:
//
//	The above copyright notice and this permission notice shall be included in all
//	copies or substantial portions of the Software.
//
//	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//	IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//	FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//	AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//	LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//	OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
//	SOFTWARE

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Azure/azure-amqp-common-go/v2/uuid"
	"github.com/devigned/tab"
	"github.com/mitchellh/mapstructure"
	"pack.ag/amqp"
)

type (
	// Message is an Service Bus message to be sent or received
	Message struct {
		ContentType      string
		CorrelationID    string
		Data             []byte
		DeliveryCount    uint32
		SessionID        *string
		GroupSequence    *uint32
		ID               string
		Label            string
		ReplyTo          string
		ReplyToGroupID   string
		To               string
		TTL              *time.Duration
		LockToken        *uuid.UUID
		SystemProperties *SystemProperties
		UserProperties   map[string]interface{}
		Format           uint32
		message          *amqp.Message
		ec               entityConnector // if an entityConnector is present, a message should send disposition via mgmt
		useSession       bool
		sessionID        *string
	}

	// DispositionAction represents the action to notify Azure Service Bus of the Message's disposition
	DispositionAction func(ctx context.Context) error

	// MessageErrorCondition represents a well-known collection of AMQP errors
	MessageErrorCondition string

	// SystemProperties are used to store properties that are set by the system.
	SystemProperties struct {
		LockedUntil            *time.Time `mapstructure:"x-opt-locked-until"`
		SequenceNumber         *int64     `mapstructure:"x-opt-sequence-number"`
		PartitionID            *int16     `mapstructure:"x-opt-partition-id"`
		PartitionKey           *string    `mapstructure:"x-opt-partition-key"`
		EnqueuedTime           *time.Time `mapstructure:"x-opt-enqueued-time"`
		DeadLetterSource       *string    `mapstructure:"x-opt-deadletter-source"`
		ScheduledEnqueueTime   *time.Time `mapstructure:"x-opt-scheduled-enqueue-time"`
		EnqueuedSequenceNumber *int64     `mapstructure:"x-opt-enqueue-sequence-number"`
		ViaPartitionKey        *string    `mapstructure:"x-opt-via-partition-key"`
	}

	mapStructureTag struct {
		Name         string
		PersistEmpty bool
	}

	dispositionStatus string

	disposition struct {
		Status                dispositionStatus
		LockTokens            []*uuid.UUID
		DeadLetterReason      *string
		DeadLetterDescription *string
	}
)

// Error Conditions
const (
	ErrorInternalError         MessageErrorCondition = "amqp:internal-error"
	ErrorNotFound              MessageErrorCondition = "amqp:not-found"
	ErrorUnauthorizedAccess    MessageErrorCondition = "amqp:unauthorized-access"
	ErrorDecodeError           MessageErrorCondition = "amqp:decode-error"
	ErrorResourceLimitExceeded MessageErrorCondition = "amqp:resource-limit-exceeded"
	ErrorNotAllowed            MessageErrorCondition = "amqp:not-allowed"
	ErrorInvalidField          MessageErrorCondition = "amqp:invalid-field"
	ErrorNotImplemented        MessageErrorCondition = "amqp:not-implemented"
	ErrorResourceLocked        MessageErrorCondition = "amqp:resource-locked"
	ErrorPreconditionFailed    MessageErrorCondition = "amqp:precondition-failed"
	ErrorResourceDeleted       MessageErrorCondition = "amqp:resource-deleted"
	ErrorIllegalState          MessageErrorCondition = "amqp:illegal-state"

	completedDisposition dispositionStatus = "completed"
	abandonedDisposition dispositionStatus = "abandoned"
	suspendedDisposition dispositionStatus = "suspended"
)

const (
	lockTokenName = "x-opt-lock-token"
)

// NewMessageFromString builds an Message from a string message
func NewMessageFromString(message string) *Message {
	return NewMessage([]byte(message))
}

// NewMessage builds an Message from a slice of data
func NewMessage(data []byte) *Message {
	return &Message{
		Data: data,
	}
}

// CompleteAction will notify Azure Service Bus that the message was successfully handled and should be deleted from the
// queue
func (m *Message) CompleteAction() DispositionAction {
	return func(ctx context.Context) error {
		_, span := m.startSpanFromContext(ctx, "sb.Message.CompleteAction")
		defer span.End()

		return m.Complete(ctx)
	}
}

// AbandonAction will notify Azure Service Bus the message failed but should be re-queued for delivery.
func (m *Message) AbandonAction() DispositionAction {
	return func(ctx context.Context) error {
		_, span := m.startSpanFromContext(ctx, "sb.Message.AbandonAction")
		defer span.End()

		return m.Abandon(ctx)
	}
}

// DeadLetterAction will notify Azure Service Bus the message failed and should not re-queued
func (m *Message) DeadLetterAction(err error) DispositionAction {
	return func(ctx context.Context) error {
		_, span := m.startSpanFromContext(ctx, "sb.Message.DeadLetterAction")
		defer span.End()

		return m.DeadLetter(ctx, err)
	}
}

// DeadLetterWithInfoAction will notify Azure Service Bus the message failed and should not be re-queued with additional
// context
func (m *Message) DeadLetterWithInfoAction(err error, condition MessageErrorCondition, additionalData map[string]string) DispositionAction {
	return func(ctx context.Context) error {
		_, span := m.startSpanFromContext(ctx, "sb.Message.DeadLetterWithInfoAction")
		defer span.End()

		return m.DeadLetterWithInfo(ctx, err, condition, additionalData)
	}
}

// Complete will notify Azure Service Bus that the message was successfully handled and should be deleted from the queue
func (m *Message) Complete(ctx context.Context) error {
	_, span := m.startSpanFromContext(ctx, "sb.Message.Complete")
	defer span.End()

	if m.ec != nil {
		return sendMgmtDisposition(ctx, m, disposition{Status: completedDisposition})
	}

	return m.message.Accept()
}

// Abandon will notify Azure Service Bus the message failed but should be re-queued for delivery.
func (m *Message) Abandon(ctx context.Context) error {
	_, span := m.startSpanFromContext(ctx, "sb.Message.Abandon")
	defer span.End()

	if m.ec != nil {
		d := disposition{
			Status: abandonedDisposition,
		}
		return sendMgmtDisposition(ctx, m, d)
	}

	return m.message.Modify(false, false, nil)
}

// Defer will set aside the message for later processing
//
// When a queue or subscription client receives a message that it is willing to process, but for which processing is
// not currently possible due to special circumstances inside of the application, it has the option of "deferring"
// retrieval of the message to a later point. The message remains in the queue or subscription, but it is set aside.
//
// Deferral is a feature specifically created for workflow processing scenarios. Workflow frameworks may require certain
// operations to be processed in a particular order, and may have to postpone processing of some received messages
// until prescribed prior work that is informed by other messages has been completed.
//
// A simple illustrative example is an order processing sequence in which a payment notification from an external
// payment provider appears in a system before the matching purchase order has been propagated from the store front
// to the fulfillment system. In that case, the fulfillment system might defer processing the payment notification
// until there is an order with which to associate it. In rendezvous scenarios, where messages from different sources
// drive a workflow forward, the real-time execution order may indeed be correct, but the messages reflecting the
// outcomes may arrive out of order.
//
// Ultimately, deferral aids in reordering messages from the arrival order into an order in which they can be
// processed, while leaving those messages safely in the message store for which processing needs to be postponed.
func (m *Message) Defer(ctx context.Context) error {
	_, span := m.startSpanFromContext(ctx, "sb.Message.Defer")
	defer span.End()

	return m.message.Modify(true, true, nil)
}

// Release will notify Azure Service Bus the message should be re-queued without failure.
//func (m *Message) Release() DispositionAction {
//	return func(ctx context.Context) {
//		span, _ := m.startSpanFromContext(ctx, "sb.Message.Release")
//		defer span.Finish()
//
//		m.message.Release()
//	}
//}

// DeadLetter will notify Azure Service Bus the message failed and should not re-queued
func (m *Message) DeadLetter(ctx context.Context, err error) error {
	_, span := m.startSpanFromContext(ctx, "sb.Message.DeadLetter")
	defer span.End()

	if m.ec != nil {
		d := disposition{
			Status:                suspendedDisposition,
			DeadLetterDescription: ptrString(err.Error()),
			DeadLetterReason:      ptrString("amqp:error"),
		}
		return sendMgmtDisposition(ctx, m, d)
	}

	amqpErr := amqp.Error{
		Condition:   amqp.ErrorCondition(ErrorInternalError),
		Description: err.Error(),
	}
	return m.message.Reject(&amqpErr)

}

// DeadLetterWithInfo will notify Azure Service Bus the message failed and should not be re-queued with additional
// context
func (m *Message) DeadLetterWithInfo(ctx context.Context, err error, condition MessageErrorCondition, additionalData map[string]string) error {
	_, span := m.startSpanFromContext(ctx, "sb.Message.DeadLetterWithInfo")
	defer span.End()

	if m.ec != nil {
		d := disposition{
			Status:                suspendedDisposition,
			DeadLetterDescription: ptrString(err.Error()),
			DeadLetterReason:      ptrString("amqp:error"),
		}
		return sendMgmtDisposition(ctx, m, d)
	}

	var info map[string]interface{}
	if additionalData != nil {
		info = make(map[string]interface{}, len(additionalData))
		for key, val := range additionalData {
			info[key] = val
		}
	}

	amqpErr := amqp.Error{
		Condition:   amqp.ErrorCondition(condition),
		Description: err.Error(),
		Info:        info,
	}
	return m.message.Reject(&amqpErr)
}

// ScheduleAt will ensure Azure Service Bus delivers the message after the time specified
// (usually within 1 minute after the specified time)
func (m *Message) ScheduleAt(t time.Time) {
	if m.SystemProperties == nil {
		m.SystemProperties = new(SystemProperties)
	}
	utcTime := t.UTC()
	m.SystemProperties.ScheduledEnqueueTime = &utcTime
}

// Set implements tab.Carrier
func (m *Message) Set(key string, value interface{}) {
	if m.UserProperties == nil {
		m.UserProperties = make(map[string]interface{})
	}
	m.UserProperties[key] = value
}

// GetKeyValues implements tab.Carrier
func (m *Message) GetKeyValues() map[string]interface{} {
	return m.UserProperties
}

func sendMgmtDisposition(ctx context.Context, m *Message, state disposition) error {
	ctx, span := startConsumerSpanFromContext(ctx, "sb.sendMgmtDisposition")
	defer span.End()

	client, err := m.ec.getEntity().GetRPCClient(ctx)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	return client.SendDisposition(ctx, m, state)
}

func (m *Message) toMsg() (*amqp.Message, error) {
	amqpMsg := m.message
	if amqpMsg == nil {
		amqpMsg = amqp.NewMessage(m.Data)
	}

	if m.TTL != nil {
		if amqpMsg.Header == nil {
			amqpMsg.Header = new(amqp.MessageHeader)
		}
		amqpMsg.Header.TTL = *m.TTL
	}

	amqpMsg.Properties = &amqp.MessageProperties{
		MessageID: m.ID,
	}

	if m.SessionID != nil {
		amqpMsg.Properties.GroupID = *m.SessionID
	}

	if m.GroupSequence != nil {
		amqpMsg.Properties.GroupSequence = *m.GroupSequence
	}

	amqpMsg.Properties.CorrelationID = m.CorrelationID
	amqpMsg.Properties.ContentType = m.ContentType
	amqpMsg.Properties.Subject = m.Label
	amqpMsg.Properties.To = m.To
	amqpMsg.Properties.ReplyTo = m.ReplyTo
	amqpMsg.Properties.ReplyToGroupID = m.ReplyToGroupID

	if len(m.UserProperties) > 0 {
		amqpMsg.ApplicationProperties = make(map[string]interface{})
		for key, value := range m.UserProperties {
			amqpMsg.ApplicationProperties[key] = value
		}
	}

	if m.SystemProperties != nil {
		sysPropMap, err := encodeStructureToMap(m.SystemProperties)
		if err != nil {
			return nil, err
		}
		amqpMsg.Annotations = annotationsFromMap(sysPropMap)
	}

	if m.LockToken != nil {
		if amqpMsg.DeliveryAnnotations == nil {
			amqpMsg.DeliveryAnnotations = make(amqp.Annotations)
		}
		amqpMsg.DeliveryAnnotations[lockTokenName] = *m.LockToken
	}

	return amqpMsg, nil
}

func annotationsFromMap(m map[string]interface{}) amqp.Annotations {
	a := make(amqp.Annotations)
	for key, val := range m {
		a[key] = val
	}
	return a
}

func messageFromAMQPMessage(msg *amqp.Message) (*Message, error) {
	return newMessage(msg.Data[0], msg)
}

func newMessage(data []byte, amqpMsg *amqp.Message) (*Message, error) {
	msg := &Message{
		Data:    data,
		message: amqpMsg,
	}

	if amqpMsg == nil {
		return msg, nil
	}

	if amqpMsg.Properties != nil {
		if id, ok := amqpMsg.Properties.MessageID.(string); ok {
			msg.ID = id
		}
		msg.SessionID = &amqpMsg.Properties.GroupID
		msg.GroupSequence = &amqpMsg.Properties.GroupSequence
		if id, ok := amqpMsg.Properties.CorrelationID.(string); ok {
			msg.CorrelationID = id
		}
		msg.ContentType = amqpMsg.Properties.ContentType
		msg.Label = amqpMsg.Properties.Subject
		msg.To = amqpMsg.Properties.To
		msg.ReplyTo = amqpMsg.Properties.ReplyTo
		msg.ReplyToGroupID = amqpMsg.Properties.ReplyToGroupID
		if amqpMsg.Header != nil {
			msg.DeliveryCount = amqpMsg.Header.DeliveryCount + 1
			msg.TTL = &amqpMsg.Header.TTL
		}
	}

	if amqpMsg.ApplicationProperties != nil {
		msg.UserProperties = make(map[string]interface{}, len(amqpMsg.ApplicationProperties))
		for key, value := range amqpMsg.ApplicationProperties {
			msg.UserProperties[key] = value
		}
	}

	if amqpMsg.Annotations != nil {
		if err := mapstructure.Decode(amqpMsg.Annotations, &msg.SystemProperties); err != nil {
			return msg, err
		}
	}

	if amqpMsg.DeliveryTag != nil && len(amqpMsg.DeliveryTag) > 0 {
		lockToken, err := lockTokenFromMessageTag(amqpMsg)
		if err != nil {
			return msg, err
		}
		msg.LockToken = lockToken
	}

	if token, ok := amqpMsg.DeliveryAnnotations[lockTokenName]; ok {
		if id, ok := token.(amqp.UUID); ok {
			sid := uuid.UUID([16]byte(id))
			msg.LockToken = &sid
		}
	}

	msg.Format = amqpMsg.Format
	return msg, nil
}

func lockTokenFromMessageTag(msg *amqp.Message) (*uuid.UUID, error) {
	return uuidFromLockTokenBytes(msg.DeliveryTag)
}

func uuidFromLockTokenBytes(bytes []byte) (*uuid.UUID, error) {
	if len(bytes) != 16 {
		return nil, fmt.Errorf("invalid lock token, token was not 16 bytes long")
	}

	var swapIndex = func(indexOne, indexTwo int, array *[16]byte) {
		v1 := array[indexOne]
		array[indexOne] = array[indexTwo]
		array[indexTwo] = v1
	}

	// Get lock token from the deliveryTag
	var lockTokenBytes [16]byte
	copy(lockTokenBytes[:], bytes[:16])
	// translate from .net guid byte serialisation format to amqp rfc standard
	swapIndex(0, 3, &lockTokenBytes)
	swapIndex(1, 2, &lockTokenBytes)
	swapIndex(4, 5, &lockTokenBytes)
	swapIndex(6, 7, &lockTokenBytes)
	amqpUUID := uuid.UUID(lockTokenBytes)

	return &amqpUUID, nil
}

func encodeStructureToMap(structPointer interface{}) (map[string]interface{}, error) {
	valueOfStruct := reflect.ValueOf(structPointer)
	s := valueOfStruct.Elem()
	if s.Kind() != reflect.Struct {
		return nil, fmt.Errorf("must provide a struct")
	}

	encoded := make(map[string]interface{})
	for i := 0; i < s.NumField(); i++ {
		f := s.Field(i)
		if f.IsValid() && f.CanSet() {
			tf := s.Type().Field(i)
			tag, err := parseMapStructureTag(tf.Tag)
			if err != nil {
				return nil, err
			}

			if tag != nil {
				switch f.Kind() {
				case reflect.Ptr:
					if !f.IsNil() || tag.PersistEmpty {
						if f.IsNil() {
							encoded[tag.Name] = nil
						} else {
							encoded[tag.Name] = f.Elem().Interface()
						}
					}
				default:
					if f.Interface() != reflect.Zero(f.Type()).Interface() || tag.PersistEmpty {
						encoded[tag.Name] = f.Interface()
					}
				}
			}
		}
	}

	return encoded, nil
}

func parseMapStructureTag(tag reflect.StructTag) (*mapStructureTag, error) {
	str, ok := tag.Lookup("mapstructure")
	if !ok {
		return nil, nil
	}

	mapTag := new(mapStructureTag)
	split := strings.Split(str, ",")
	mapTag.Name = strings.TrimSpace(split[0])

	if len(split) > 1 {
		for _, tagKey := range split[1:] {
			switch tagKey {
			case "persistempty":
				mapTag.PersistEmpty = true
			default:
				return nil, fmt.Errorf("key %q is not understood", tagKey)
			}
		}
	}
	return mapTag, nil
}
