package servicebus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-amqp-common-go/v2/rpc"
	"github.com/devigned/tab"
	"pack.ag/amqp"
)

// MessageSession represents and allows for interaction with a Service Bus Session.
type MessageSession struct {
	*Receiver
	entity         EntityManagementAddresser
	mu             sync.RWMutex
	sessionID      *string
	lockExpiration time.Time
	done           chan struct {
	}
	cancel sync.Once
}

func newMessageSession(r *Receiver, entity EntityManagementAddresser, sessionID *string) (retval *MessageSession, _ error) {
	retval = &MessageSession{
		Receiver:       r,
		entity:         entity,
		sessionID:      sessionID,
		lockExpiration: time.Now(),
		done:           make(chan struct{}),
	}

	return
}

// Close communicates that Handler receiving messages should no longer continue to be executed. This can happen when:
// - A Handler recognizes that no further messages will come to this session.
// - A Handler has given up on receiving more messages before a session. Future messages should be delegated to the next
//   available session client.
func (ms *MessageSession) Close() {
	ms.cancel.Do(func() {
		close(ms.done)
	})
}

// LockedUntil fetches the moment in time when the Session lock held by this Receiver will expire.
func (ms *MessageSession) LockedUntil() time.Time {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	return ms.lockExpiration
}

// RenewLock requests that the Service Bus Server renews this client's lock on an existing Session.
func (ms *MessageSession) RenewLock(ctx context.Context) error {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	link, err := rpc.NewLinkWithSession(ms.Receiver.session.Session, ms.entity.ManagementPath())
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			"operation": "com.microsoft:renew-session-lock",
		},
		Value: map[string]interface{}{
			"session-id": ms.SessionID(),
		},
	}

	if deadline, ok := ctx.Deadline(); ok {
		msg.ApplicationProperties["com.microsoft:server-timeout"] = uint(time.Until(deadline) / time.Millisecond)
	}

	resp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		tab.For(ctx).Error(err)
		return err
	}

	if rawMessageValue, ok := resp.Message.Value.(map[string]interface{}); ok {
		if rawExpiration, ok := rawMessageValue["expiration"]; ok {
			if ms.lockExpiration, ok = rawExpiration.(time.Time); ok {
				return nil
			}
			return errors.New("\"expiration\" not of expected type time.Time")
		}
		return errors.New("missing expected property \"expiration\" in \"Value\"")

	}
	return errors.New("value not of expected type map[string]interface{}")
}

// ListSessions will list all of the sessions available
func (ms *MessageSession) ListSessions(ctx context.Context) ([]byte, error) {
	link, err := rpc.NewLink(ms.Receiver.client, ms.entity.ManagementPath())
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			"operation": "com.microsoft:get-message-sessions",
		},
		Value: map[string]interface{}{
			"last-updated-time": time.Now().UTC().Add(-30 * time.Minute),
			"skip":              int32(0),
			"top":               int32(100),
		},
	}

	rsp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	if rsp.Code != 200 {
		err := fmt.Errorf("amqp error (%d): %q", rsp.Code, rsp.Description)
		tab.For(ctx).Error(err)
		return nil, err
	}

	return rsp.Message.Data[0], nil
}

// SetState updates the current State associated with this Session.
func (ms *MessageSession) SetState(ctx context.Context, state []byte) error {
	link, err := rpc.NewLinkWithSession(ms.Receiver.session.Session, ms.entity.ManagementPath())
	if err != nil {
		return err
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			"operation": "com.microsoft:set-session-state",
		},
		Properties: &amqp.MessageProperties{
			GroupID: *ms.SessionID(),
		},
		Value: map[string]interface{}{
			"session-id":    ms.SessionID(),
			"session-state": state,
		},
	}

	rsp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		return err
	}

	if rsp.Code != 200 {
		return fmt.Errorf("amqp error (%d): %q", rsp.Code, rsp.Description)
	}
	return nil
}

// State retrieves the current State associated with this Session.
// https://docs.microsoft.com/en-us/azure/service-bus-messaging/service-bus-amqp-request-response#get-session-state
func (ms *MessageSession) State(ctx context.Context) ([]byte, error) {
	const sessionStateField = "session-state"

	link, err := rpc.NewLinkWithSession(ms.Receiver.session.Session, ms.entity.ManagementPath())
	if err != nil {
		return []byte{}, err
	}

	msg := &amqp.Message{
		ApplicationProperties: map[string]interface{}{
			"operation": "com.microsoft:get-session-state",
		},
		Value: map[string]interface{}{
			"session-id": ms.SessionID(),
		},
	}

	rsp, err := link.RetryableRPC(ctx, 5, 5*time.Second, msg)
	if err != nil {
		return []byte{}, err
	}

	if rsp.Code != 200 {
		return []byte{}, fmt.Errorf("amqp error (%d): %q", rsp.Code, rsp.Description)
	}

	if val, ok := rsp.Message.Value.(map[string]interface{}); ok {
		if rawState, ok := val[sessionStateField]; ok {
			if state, ok := rawState.([]byte); ok || rawState == nil {
				return state, nil
			}
			return nil, newErrIncorrectType(sessionStateField, []byte{}, rawState)
		}
		return nil, ErrMissingField(sessionStateField)
	}
	return nil, newErrIncorrectType("value", map[string]interface{}{}, rsp.Message.Value)
}

// SessionID gets the unique identifier of the session being interacted with by this MessageSession.
func (ms *MessageSession) SessionID() *string {
	return ms.sessionID
}
