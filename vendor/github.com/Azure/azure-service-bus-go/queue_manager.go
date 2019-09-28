package servicebus

import (
	"context"
	"encoding/xml"
	"errors"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/devigned/tab"

	"github.com/Azure/azure-service-bus-go/atom"
)

type (
	// QueueManager provides CRUD functionality for Service Bus Queues
	QueueManager struct {
		*entityManager
	}

	// Entity is represents the most basic form of an Azure Service Bus entity.
	Entity struct {
		Name string
		ID   string
	}

	// QueueEntity is the Azure Service Bus description of a Queue for management activities
	QueueEntity struct {
		*QueueDescription
		*Entity
	}

	// queueFeed is a specialized feed containing QueueEntries
	queueFeed struct {
		*atom.Feed
		Entries []queueEntry `xml:"entry"`
	}

	// queueEntry is a specialized Queue feed entry
	queueEntry struct {
		*atom.Entry
		Content *queueContent `xml:"content"`
	}

	// QueueManagementOption represents named configuration options for queue mutation
	QueueManagementOption func(*QueueDescription) error

	// Targetable provides the ability to forward messages to the entity
	Targetable interface {
		TargetURI() string
	}
)

// TargetURI provides an absolute address to a target entity
func (e Entity) TargetURI() string {
	split := strings.Split(e.ID, "?")
	return split[0]
}

func queueEntryToEntity(entry *queueEntry) *QueueEntity {
	return &QueueEntity{
		QueueDescription: &entry.Content.QueueDescription,
		Entity: &Entity{
			Name: entry.Title,
			ID:   entry.ID,
		},
	}
}

/*
QueueEntityWithPartitioning ensure the created queue will be a partitioned queue. Partitioned queues offer increased
storage and availability compared to non-partitioned queues with the trade-off of requiring the following to ensure
FIFO message retrieval:

SessionId. If a message has the SessionId property set, then Service Bus uses the SessionId property as the
partition key. This way, all messages that belong to the same session are assigned to the same fragment and handled
by the same message broker. This allows Service Bus to guarantee message ordering as well as the consistency of
session states.

PartitionKey. If a message has the PartitionKey property set but not the SessionId property, then Service Bus uses
the PartitionKey property as the partition key. Use the PartitionKey property to send non-sessionful transactional
messages. The partition key ensures that all messages that are sent within a transaction are handled by the same
messaging broker.

MessageId. If the queue has the RequiresDuplicationDetection property set to true, then the MessageId
property serves as the partition key if the SessionId or a PartitionKey properties are not set. This ensures that
all copies of the same message are handled by the same message broker and, thus, allows Service Bus to detect and
eliminate duplicate messages
*/
func QueueEntityWithPartitioning() QueueManagementOption {
	return func(queue *QueueDescription) error {
		queue.EnablePartitioning = ptrBool(true)
		return nil
	}
}

// QueueEntityWithMaxSizeInMegabytes configures the maximum size of the queue in megabytes (1 * 1024 - 5 * 1024), which is the size of
// the memory allocated for the queue. Default is 1 MB (1 * 1024).
//
// size must be between 1024 and 5 * 1024 for the Standard sku and up to 80 * 1024 for Premium sku
func QueueEntityWithMaxSizeInMegabytes(size int) QueueManagementOption {
	return func(q *QueueDescription) error {
		if size < 1024 || size > 80*1024 {
			return errors.New("QueueEntityWithMaxSizeInMegabytes: must be between 1024 and 5 * 1024 for the Standard sku and up to 80 * 1024 for Premium sku")
		}
		int32Size := int32(size)
		q.MaxSizeInMegabytes = &int32Size
		return nil
	}
}

// QueueEntityWithDuplicateDetection configures the queue to detect duplicates for a given time window. If window
// is not specified, then it uses the default of 10 minutes.
func QueueEntityWithDuplicateDetection(window *time.Duration) QueueManagementOption {
	return func(q *QueueDescription) error {
		q.RequiresDuplicateDetection = ptrBool(true)
		if window != nil {
			q.DuplicateDetectionHistoryTimeWindow = ptrString(durationTo8601Seconds(*window))
		}
		return nil
	}
}

// QueueEntityWithRequiredSessions will ensure the queue requires senders and receivers to have sessionIDs
func QueueEntityWithRequiredSessions() QueueManagementOption {
	return func(q *QueueDescription) error {
		q.RequiresSession = ptrBool(true)
		return nil
	}
}

// QueueEntityWithDeadLetteringOnMessageExpiration will ensure the queue sends expired messages to the dead letter queue
func QueueEntityWithDeadLetteringOnMessageExpiration() QueueManagementOption {
	return func(q *QueueDescription) error {
		q.DeadLetteringOnMessageExpiration = ptrBool(true)
		return nil
	}
}

// QueueEntityWithAutoDeleteOnIdle configures the queue to automatically delete after the specified idle interval. The
// minimum duration is 5 minutes.
func QueueEntityWithAutoDeleteOnIdle(window *time.Duration) QueueManagementOption {
	return func(q *QueueDescription) error {
		if window != nil {
			if window.Minutes() < 5 {
				return errors.New("QueueEntityWithAutoDeleteOnIdle: window must be greater than 5 minutes")
			}
			q.AutoDeleteOnIdle = ptrString(durationTo8601Seconds(*window))
		}
		return nil
	}
}

// QueueEntityWithMessageTimeToLive configures the queue to set a time to live on messages. This is the duration after which
// the message expires, starting from when the message is sent to Service Bus. This is the default value used when
// TimeToLive is not set on a message itself. If nil, defaults to 14 days.
func QueueEntityWithMessageTimeToLive(window *time.Duration) QueueManagementOption {
	return func(q *QueueDescription) error {
		if window == nil {
			duration := time.Duration(14 * 24 * time.Hour)
			window = &duration
		}
		q.DefaultMessageTimeToLive = ptrString(durationTo8601Seconds(*window))
		return nil
	}
}

// QueueEntityWithLockDuration configures the queue to have a duration of a peek-lock; that is, the amount of time that the
// message is locked for other receivers. The maximum value for LockDuration is 5 minutes; the default value is 1
// minute.
func QueueEntityWithLockDuration(window *time.Duration) QueueManagementOption {
	return func(q *QueueDescription) error {
		if window == nil {
			duration := time.Duration(1 * time.Minute)
			window = &duration
		}
		q.LockDuration = ptrString(durationTo8601Seconds(*window))
		return nil
	}
}

// QueueEntityWithAutoForward configures the queue to automatically forward messages to the specified target.
//
// The ability to AutoForward to a target requires the connection have management authorization. If the connection
// string or Azure Active Directory identity used does not have management authorization, an unauthorized error will be
// returned on the PUT.
func QueueEntityWithAutoForward(target Targetable) QueueManagementOption {
	return func(q *QueueDescription) error {
		uri := target.TargetURI()
		q.ForwardTo = &uri
		return nil
	}
}

// QueueEntityWithForwardDeadLetteredMessagesTo configures the queue to automatically forward dead letter messages to
// the specified target.
//
// The ability to forward dead letter messages to a target requires the connection have management authorization. If
// the connection string or Azure Active Directory identity used does not have management authorization, an unauthorized
// error will be returned on the PUT.
func QueueEntityWithForwardDeadLetteredMessagesTo(target Targetable) QueueManagementOption {
	return func(q *QueueDescription) error {
		uri := target.TargetURI()
		q.ForwardDeadLetteredMessagesTo = &uri
		return nil
	}
}

// QueueEntityWithMaxDeliveryCount configures the queue to have a maximum number of delivery attempts before
// dead-lettering the message
func QueueEntityWithMaxDeliveryCount(count int32) QueueManagementOption {
	return func(q *QueueDescription) error {
		q.MaxDeliveryCount = &count
		return nil
	}
}

// NewQueueManager creates a new QueueManager for a Service Bus Namespace
func (ns *Namespace) NewQueueManager() *QueueManager {
	return &QueueManager{
		entityManager: newEntityManager(ns.getHTTPSHostURI(), ns.TokenProvider),
	}
}

// Delete deletes a Service Bus Queue entity by name
func (qm *QueueManager) Delete(ctx context.Context, name string) error {
	ctx, span := qm.startSpanFromContext(ctx, "sb.QueueManager.Delete")
	defer span.End()

	res, err := qm.entityManager.Delete(ctx, "/"+name)
	defer closeRes(ctx, res)

	return err
}

// Put creates or updates a Service Bus Queue
func (qm *QueueManager) Put(ctx context.Context, name string, opts ...QueueManagementOption) (*QueueEntity, error) {
	ctx, span := qm.startSpanFromContext(ctx, "sb.QueueManager.Put")
	defer span.End()

	qd := new(QueueDescription)
	for _, opt := range opts {
		if err := opt(qd); err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}
	}

	qd.ServiceBusSchema = to.StringPtr(serviceBusSchema)

	qe := &queueEntry{
		Entry: &atom.Entry{
			AtomSchema: atomSchema,
		},
		Content: &queueContent{
			Type:             applicationXML,
			QueueDescription: *qd,
		},
	}

	var mw []MiddlewareFunc
	if qd.ForwardTo != nil {
		mw = append(mw, addSupplementalAuthorization(*qd.ForwardTo, qm.TokenProvider()))
	}

	if qd.ForwardDeadLetteredMessagesTo != nil {
		mw = append(mw, addDeadLetterSupplementalAuthorization(*qd.ForwardDeadLetteredMessagesTo, qm.TokenProvider()))
	}

	reqBytes, err := xml.Marshal(qe)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	reqBytes = xmlDoc(reqBytes)
	res, err := qm.entityManager.Put(ctx, "/"+name, reqBytes, mw...)
	defer closeRes(ctx, res)

	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	var entry queueEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		return nil, formatManagementError(b)
	}
	return queueEntryToEntity(&entry), nil
}

// List fetches all of the queues for a Service Bus Namespace
func (qm *QueueManager) List(ctx context.Context) ([]*QueueEntity, error) {
	ctx, span := qm.startSpanFromContext(ctx, "sb.QueueManager.List")
	defer span.End()

	res, err := qm.entityManager.Get(ctx, `/$Resources/Queues`)
	defer closeRes(ctx, res)

	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	var feed queueFeed
	err = xml.Unmarshal(b, &feed)
	if err != nil {
		return nil, formatManagementError(b)
	}

	qd := make([]*QueueEntity, len(feed.Entries))
	for idx, entry := range feed.Entries {
		qd[idx] = queueEntryToEntity(&entry)
	}
	return qd, nil
}

// Get fetches a Service Bus Queue entity by name
func (qm *QueueManager) Get(ctx context.Context, name string) (*QueueEntity, error) {
	ctx, span := qm.startSpanFromContext(ctx, "sb.QueueManager.Get")
	defer span.End()

	res, err := qm.entityManager.Get(ctx, name)
	defer closeRes(ctx, res)

	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	var entry queueEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		if isEmptyFeed(b) {
			return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
		}
		return nil, formatManagementError(b)
	}

	return queueEntryToEntity(&entry), nil
}
