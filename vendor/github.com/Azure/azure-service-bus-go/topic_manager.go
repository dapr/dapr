package servicebus

import (
	"context"
	"encoding/xml"
	"errors"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/Azure/go-autorest/autorest/to"
	"github.com/devigned/tab"

	"github.com/Azure/azure-service-bus-go/atom"
)

type (
	// TopicManager provides CRUD functionality for Service Bus Topics
	TopicManager struct {
		*entityManager
	}

	// TopicEntity is the Azure Service Bus description of a Topic for management activities
	TopicEntity struct {
		*TopicDescription
		*Entity
	}

	// topicEntry is a specialized Topic feed entry
	topicEntry struct {
		*atom.Entry
		Content *topicContent `xml:"content"`
	}

	// topicContent is a specialized Topic body for an Atom entry
	topicContent struct {
		XMLName          xml.Name         `xml:"content"`
		Type             string           `xml:"type,attr"`
		TopicDescription TopicDescription `xml:"TopicDescription"`
	}

	// topicFeed is a specialized feed containing Topic Entries
	topicFeed struct {
		*atom.Feed
		Entries []topicEntry `xml:"entry"`
	}

	// TopicManagementOption represents named options for assisting Topic creation
	TopicManagementOption func(*TopicDescription) error
)

// NewTopicManager creates a new TopicManager for a Service Bus Namespace
func (ns *Namespace) NewTopicManager() *TopicManager {
	return &TopicManager{
		entityManager: newEntityManager(ns.getHTTPSHostURI(), ns.TokenProvider),
	}
}

// Delete deletes a Service Bus Topic entity by name
func (tm *TopicManager) Delete(ctx context.Context, name string) error {
	ctx, span := tm.startSpanFromContext(ctx, "sb.TopicManager.Delete")
	defer span.End()

	res, err := tm.entityManager.Delete(ctx, "/"+name)
	defer closeRes(ctx, res)

	return err
}

// Put creates or updates a Service Bus Topic
func (tm *TopicManager) Put(ctx context.Context, name string, opts ...TopicManagementOption) (*TopicEntity, error) {
	ctx, span := tm.startSpanFromContext(ctx, "sb.TopicManager.Put")
	defer span.End()

	td := new(TopicDescription)
	for _, opt := range opts {
		if err := opt(td); err != nil {
			tab.For(ctx).Error(err)
			return nil, err
		}
	}

	td.ServiceBusSchema = to.StringPtr(serviceBusSchema)

	qe := &topicEntry{
		Entry: &atom.Entry{
			AtomSchema: atomSchema,
		},
		Content: &topicContent{
			Type:             applicationXML,
			TopicDescription: *td,
		},
	}

	reqBytes, err := xml.Marshal(qe)
	if err != nil {
		tab.For(ctx).Error(err)
		return nil, err
	}

	reqBytes = xmlDoc(reqBytes)
	res, err := tm.entityManager.Put(ctx, "/"+name, reqBytes)
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

	var entry topicEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		return nil, formatManagementError(b)
	}
	return topicEntryToEntity(&entry), nil
}

// List fetches all of the Topics for a Service Bus Namespace
func (tm *TopicManager) List(ctx context.Context) ([]*TopicEntity, error) {
	ctx, span := tm.startSpanFromContext(ctx, "sb.TopicManager.List")
	defer span.End()

	res, err := tm.entityManager.Get(ctx, `/$Resources/Topics`)
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

	var feed topicFeed
	err = xml.Unmarshal(b, &feed)
	if err != nil {
		return nil, formatManagementError(b)
	}

	topics := make([]*TopicEntity, len(feed.Entries))
	for idx, entry := range feed.Entries {
		topics[idx] = topicEntryToEntity(&entry)
	}
	return topics, nil
}

// Get fetches a Service Bus Topic entity by name
func (tm *TopicManager) Get(ctx context.Context, name string) (*TopicEntity, error) {
	ctx, span := tm.startSpanFromContext(ctx, "sb.TopicManager.Get")
	defer span.End()

	res, err := tm.entityManager.Get(ctx, name)
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

	var entry topicEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		if isEmptyFeed(b) {
			return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
		}
		return nil, formatManagementError(b)
	}
	return topicEntryToEntity(&entry), nil
}

func topicEntryToEntity(entry *topicEntry) *TopicEntity {
	return &TopicEntity{
		TopicDescription: &entry.Content.TopicDescription,
		Entity: &Entity{
			Name: entry.Title,
			ID:   entry.ID,
		},
	}
}

// TopicWithMaxSizeInMegabytes configures the maximum size of the topic in megabytes (1 * 1024 - 5 * 1024), which is the size of
// the memory allocated for the topic. Default is 1 MB (1 * 1024).
//
// size must be between 1024 and 5 * 1024 for the Standard sku and up to 80 * 1024 for Premium sku
func TopicWithMaxSizeInMegabytes(size int) TopicManagementOption {
	return func(t *TopicDescription) error {
		if size < 1024 || size > 80*1024 {
			return errors.New("TopicWithMaxSizeInMegabytes: must be between 1024 and 5 * 1024 for the Standard sku and up to 80 * 1024 for Premium sku")
		}
		size32 := int32(size)
		t.MaxSizeInMegabytes = &size32
		return nil
	}
}

// TopicWithPartitioning configures the topic to be partitioned across multiple message brokers.
func TopicWithPartitioning() TopicManagementOption {
	return func(t *TopicDescription) error {
		t.EnablePartitioning = ptrBool(true)
		return nil
	}
}

// TopicWithOrdering configures the topic to support ordering of messages.
func TopicWithOrdering() TopicManagementOption {
	return func(t *TopicDescription) error {
		t.SupportOrdering = ptrBool(true)
		return nil
	}
}

// TopicWithDuplicateDetection configures the topic to detect duplicates for a given time window. If window
// is not specified, then it uses the default of 10 minutes.
func TopicWithDuplicateDetection(window *time.Duration) TopicManagementOption {
	return func(t *TopicDescription) error {
		t.RequiresDuplicateDetection = ptrBool(true)
		if window != nil {
			t.DuplicateDetectionHistoryTimeWindow = ptrString(durationTo8601Seconds(*window))
		}
		return nil
	}
}

// TopicWithExpress configures the topic to hold a message in memory temporarily before writing it to persistent storage.
func TopicWithExpress() TopicManagementOption {
	return func(t *TopicDescription) error {
		t.EnableExpress = ptrBool(true)
		return nil
	}
}

// TopicWithBatchedOperations configures the topic to batch server-side operations.
func TopicWithBatchedOperations() TopicManagementOption {
	return func(t *TopicDescription) error {
		t.EnableBatchedOperations = ptrBool(true)
		return nil
	}
}

// TopicWithAutoDeleteOnIdle configures the topic to automatically delete after the specified idle interval. The
// minimum duration is 5 minutes.
func TopicWithAutoDeleteOnIdle(window *time.Duration) TopicManagementOption {
	return func(t *TopicDescription) error {
		if window != nil {
			if window.Minutes() < 5 {
				return errors.New("TopicWithAutoDeleteOnIdle: window must be greater than 5 minutes")
			}
			t.AutoDeleteOnIdle = ptrString(durationTo8601Seconds(*window))
		}
		return nil
	}
}

// TopicWithMessageTimeToLive configures the topic to set a time to live on messages. This is the duration after which
// the message expires, starting from when the message is sent to Service Bus. This is the default value used when
// TimeToLive is not set on a message itself. If nil, defaults to 14 days.
func TopicWithMessageTimeToLive(window *time.Duration) TopicManagementOption {
	return func(t *TopicDescription) error {
		if window == nil {
			duration := time.Duration(14 * 24 * time.Hour)
			window = &duration
		}
		t.DefaultMessageTimeToLive = ptrString(durationTo8601Seconds(*window))
		return nil
	}
}
