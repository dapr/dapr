package servicebus

import (
	"context"
	"encoding/xml"
	"errors"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/go-autorest/autorest/date"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/devigned/tab"

	"github.com/Azure/azure-service-bus-go/atom"
)

type (
	// SubscriptionManager provides CRUD functionality for Service Bus Subscription
	SubscriptionManager struct {
		*entityManager
		Topic *Topic
	}

	// FilterDescriber can transform itself into a FilterDescription
	FilterDescriber interface {
		ToFilterDescription() FilterDescription
	}

	// ActionDescriber can transform itself into a ActionDescription
	ActionDescriber interface {
		ToActionDescription() ActionDescription
	}

	// RuleDescription is the content type for Subscription Rule management requests
	RuleDescription struct {
		XMLName xml.Name `xml:"RuleDescription"`
		BaseEntityDescription
		CreatedAt *date.Time         `xml:"CreatedAt,omitempty"`
		Filter    FilterDescription  `xml:"Filter"`
		Action    *ActionDescription `xml:"Action,omitempty"`
	}

	// FilterDescription describes a filter which can be applied to a subscription to filter messages from the topic.
	//
	// Subscribers can define which messages they want to receive from a topic. These messages are specified in the
	// form of one or more named subscription rules. Each rule consists of a condition that selects particular messages
	// and an action that annotates the selected message. For each matching rule condition, the subscription produces a
	// copy of the message, which may be differently annotated for each matching rule.
	//
	// Each newly created topic subscription has an initial default subscription rule. If you don't explicitly specify a
	// filter condition for the rule, the applied filter is the true filter that enables all messages to be selected
	// into the subscription. The default rule has no associated annotation action.
	FilterDescription struct {
		XMLName xml.Name `xml:"Filter"`
		CorrelationFilter
		Type               string  `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
		SQLExpression      *string `xml:"SqlExpression,omitempty"`
		CompatibilityLevel int     `xml:"CompatibilityLevel,omitempty"`
	}

	// ActionDescription describes an action upon a message that matches a filter
	//
	// With SQL filter conditions, you can define an action that can annotate the message by adding, removing, or
	// replacing properties and their values. The action uses a SQL-like expression that loosely leans on the SQL
	// UPDATE statement syntax. The action is performed on the message after it has been matched and before the message
	// is selected into the subscription. The changes to the message properties are private to the message copied into
	// the subscription.
	ActionDescription struct {
		Type                  string `xml:"http://www.w3.org/2001/XMLSchema-instance type,attr"`
		SQLExpression         string `xml:"SqlExpression"`
		RequiresPreprocessing bool   `xml:"RequiresPreprocessing"`
		CompatibilityLevel    int    `xml:"CompatibilityLevel,omitempty"`
	}

	// RuleEntity is the Azure Service Bus description of a Subscription Rule for management activities
	RuleEntity struct {
		*RuleDescription
		*Entity
	}

	// ruleContent is a specialized Subscription body for an Atom entry
	ruleContent struct {
		XMLName         xml.Name        `xml:"content"`
		Type            string          `xml:"type,attr"`
		RuleDescription RuleDescription `xml:"RuleDescription"`
	}

	ruleEntry struct {
		*atom.Entry
		Content *ruleContent `xml:"content"`
	}

	ruleFeed struct {
		*atom.Feed
		Entries []ruleEntry `xml:"entry"`
	}

	// SubscriptionDescription is the content type for Subscription management requests
	SubscriptionDescription struct {
		XMLName xml.Name `xml:"SubscriptionDescription"`
		BaseEntityDescription
		LockDuration                              *string       `xml:"LockDuration,omitempty"` // LockDuration - ISO 8601 timespan duration of a peek-lock; that is, the amount of time that the message is locked for other receivers. The maximum value for LockDuration is 5 minutes; the default value is 1 minute.
		RequiresSession                           *bool         `xml:"RequiresSession,omitempty"`
		DefaultMessageTimeToLive                  *string       `xml:"DefaultMessageTimeToLive,omitempty"`         // DefaultMessageTimeToLive - ISO 8601 default message timespan to live value. This is the duration after which the message expires, starting from when the message is sent to Service Bus. This is the default value used when TimeToLive is not set on a message itself.
		DeadLetteringOnMessageExpiration          *bool         `xml:"DeadLetteringOnMessageExpiration,omitempty"` // DeadLetteringOnMessageExpiration - A value that indicates whether this queue has dead letter support when a message expires.
		DeadLetteringOnFilterEvaluationExceptions *bool         `xml:"DeadLetteringOnFilterEvaluationExceptions,omitempty"`
		MessageCount                              *int64        `xml:"MessageCount,omitempty"`            // MessageCount - The number of messages in the queue.
		MaxDeliveryCount                          *int32        `xml:"MaxDeliveryCount,omitempty"`        // MaxDeliveryCount - The maximum delivery count. A message is automatically deadlettered after this number of deliveries. default value is 10.
		EnableBatchedOperations                   *bool         `xml:"EnableBatchedOperations,omitempty"` // EnableBatchedOperations - Value that indicates whether server-side batched operations are enabled.
		Status                                    *EntityStatus `xml:"Status,omitempty"`
		CreatedAt                                 *date.Time    `xml:"CreatedAt,omitempty"`
		UpdatedAt                                 *date.Time    `xml:"UpdatedAt,omitempty"`
		AccessedAt                                *date.Time    `xml:"AccessedAt,omitempty"`
		AutoDeleteOnIdle                          *string       `xml:"AutoDeleteOnIdle,omitempty"`
		ForwardTo                                 *string       `xml:"ForwardTo,omitempty"`                     // ForwardTo - absolute URI of the entity to forward messages
		ForwardDeadLetteredMessagesTo             *string       `xml:"ForwardDeadLetteredMessagesTo,omitempty"` // ForwardDeadLetteredMessagesTo - absolute URI of the entity to forward dead letter messages
		CountDetails                              *CountDetails `xml:"CountDetails,omitempty"`
	}

	// SubscriptionEntity is the Azure Service Bus description of a topic Subscription for management activities
	SubscriptionEntity struct {
		*SubscriptionDescription
		*Entity
	}

	// subscriptionFeed is a specialized feed containing Topic Subscriptions
	subscriptionFeed struct {
		*atom.Feed
		Entries []subscriptionEntry `xml:"entry"`
	}

	// subscriptionEntryContent is a specialized Topic feed Subscription
	subscriptionEntry struct {
		*atom.Entry
		Content *subscriptionContent `xml:"content"`
	}

	// subscriptionContent is a specialized Subscription body for an Atom entry
	subscriptionContent struct {
		XMLName                 xml.Name                `xml:"content"`
		Type                    string                  `xml:"type,attr"`
		SubscriptionDescription SubscriptionDescription `xml:"SubscriptionDescription"`
	}

	// SubscriptionManagementOption represents named options for assisting Subscription creation
	SubscriptionManagementOption func(*SubscriptionDescription) error
)

// NewSubscriptionManager creates a new SubscriptionManager for a Service Bus Topic
func (t *Topic) NewSubscriptionManager() *SubscriptionManager {
	return &SubscriptionManager{
		entityManager: newEntityManager(t.namespace.getHTTPSHostURI(), t.namespace.TokenProvider),
		Topic:         t,
	}
}

// NewSubscriptionManager creates a new SubscriptionManger for a Service Bus Namespace
func (ns *Namespace) NewSubscriptionManager(topicName string) (*SubscriptionManager, error) {
	t, err := ns.NewTopic(topicName)
	if err != nil {
		return nil, err
	}
	return &SubscriptionManager{
		entityManager: newEntityManager(t.namespace.getHTTPSHostURI(), t.namespace.TokenProvider),
		Topic:         t,
	}, nil
}

// Delete deletes a Service Bus Topic entity by name
func (sm *SubscriptionManager) Delete(ctx context.Context, name string) error {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.Delete")
	defer span.End()

	res, err := sm.entityManager.Delete(ctx, sm.getResourceURI(name))
	defer closeRes(ctx, res)

	return err
}

// Put creates or updates a Service Bus Topic
func (sm *SubscriptionManager) Put(ctx context.Context, name string, opts ...SubscriptionManagementOption) (*SubscriptionEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.Put")
	defer span.End()

	sd := new(SubscriptionDescription)
	for _, opt := range opts {
		if err := opt(sd); err != nil {
			return nil, err
		}
	}

	sd.ServiceBusSchema = to.StringPtr(serviceBusSchema)

	qe := &subscriptionEntry{
		Entry: &atom.Entry{
			AtomSchema: atomSchema,
		},
		Content: &subscriptionContent{
			Type:                    applicationXML,
			SubscriptionDescription: *sd,
		},
	}

	var mw []MiddlewareFunc
	if sd.ForwardTo != nil {
		mw = append(mw, addSupplementalAuthorization(*sd.ForwardTo, sm.TokenProvider()))
	}

	if sd.ForwardDeadLetteredMessagesTo != nil {
		mw = append(mw, addDeadLetterSupplementalAuthorization(*sd.ForwardDeadLetteredMessagesTo, sm.TokenProvider()))
	}

	reqBytes, err := xml.Marshal(qe)
	if err != nil {
		return nil, err
	}

	reqBytes = xmlDoc(reqBytes)
	res, err := sm.entityManager.Put(ctx, sm.getResourceURI(name), reqBytes, mw...)
	defer closeRes(ctx, res)

	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var entry subscriptionEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		return nil, formatManagementError(b)
	}
	return subscriptionEntryToEntity(&entry), nil
}

// List fetches all of the Topics for a Service Bus Namespace
func (sm *SubscriptionManager) List(ctx context.Context) ([]*SubscriptionEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.List")
	defer span.End()

	res, err := sm.entityManager.Get(ctx, "/"+sm.Topic.Name+"/subscriptions")
	defer closeRes(ctx, res)

	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var feed subscriptionFeed
	err = xml.Unmarshal(b, &feed)
	if err != nil {
		return nil, formatManagementError(b)
	}

	subs := make([]*SubscriptionEntity, len(feed.Entries))
	for idx, entry := range feed.Entries {
		subs[idx] = subscriptionEntryToEntity(&entry)
	}
	return subs, nil
}

// Get fetches a Service Bus Topic entity by name
func (sm *SubscriptionManager) Get(ctx context.Context, name string) (*SubscriptionEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.Get")
	defer span.End()

	res, err := sm.entityManager.Get(ctx, sm.getResourceURI(name))
	defer closeRes(ctx, res)

	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var entry subscriptionEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		if isEmptyFeed(b) {
			// seems the only way to catch 404 is if the feed is empty. If no subscriptions exist, the GET returns 200
			// and an empty feed.
			return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
		}
		return nil, formatManagementError(b)
	}
	return subscriptionEntryToEntity(&entry), nil
}

// ListRules returns the slice of subscription filter rules
//
// By default when the subscription is created, there exists a single "true" filter which matches all messages.
func (sm *SubscriptionManager) ListRules(ctx context.Context, subscriptionName string) ([]*RuleEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.ListRules")
	defer span.End()

	res, err := sm.entityManager.Get(ctx, sm.getRulesResourceURI(subscriptionName))
	defer closeRes(ctx, res)

	if err != nil {
		return nil, err
	}

	if res.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound{EntityPath: res.Request.URL.Path}
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var feed ruleFeed
	err = xml.Unmarshal(b, &feed)
	if err != nil {
		return nil, formatManagementError(b)
	}

	rules := make([]*RuleEntity, len(feed.Entries))
	for idx, entry := range feed.Entries {
		rules[idx] = ruleEntryToEntity(&entry)
	}
	return rules, nil
}

// PutRuleWithAction creates a new Subscription rule to filter messages from the topic and then perform an action
func (sm *SubscriptionManager) PutRuleWithAction(ctx context.Context, subscriptionName, ruleName string, filter FilterDescriber, action ActionDescriber) (*RuleEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.PutRuleWithAction")
	defer span.End()

	ad := action.ToActionDescription()
	rd := &RuleDescription{
		BaseEntityDescription: BaseEntityDescription{
			ServiceBusSchema:       to.StringPtr(serviceBusSchema),
			InstanceMetadataSchema: to.StringPtr(schemaInstance),
		},
		Filter: filter.ToFilterDescription(),
		Action: &ad,
	}

	return sm.putRule(ctx, subscriptionName, ruleName, rd)
}

// PutRule creates a new Subscription rule to filter messages from the topic
func (sm *SubscriptionManager) PutRule(ctx context.Context, subscriptionName, ruleName string, filter FilterDescriber) (*RuleEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.PutRule")
	defer span.End()

	rd := &RuleDescription{
		BaseEntityDescription: BaseEntityDescription{
			ServiceBusSchema:       to.StringPtr(serviceBusSchema),
			InstanceMetadataSchema: to.StringPtr(schemaInstance),
		},
		Filter: filter.ToFilterDescription(),
	}

	return sm.putRule(ctx, subscriptionName, ruleName, rd)
}

func (sm *SubscriptionManager) putRule(ctx context.Context, subscriptionName, ruleName string, rd *RuleDescription) (*RuleEntity, error) {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.putRule")
	defer span.End()

	re := &ruleEntry{
		Entry: &atom.Entry{
			AtomSchema: atomSchema,
		},
		Content: &ruleContent{
			Type:            applicationXML,
			RuleDescription: *rd,
		},
	}

	reqBytes, err := xml.Marshal(re)
	if err != nil {
		return nil, err
	}

	// TODO: fix the unmarshal / marshal of xml with this attribute or ask the service to fix it. This is sad, but works.
	str := string(reqBytes)
	str = strings.Replace(str, `xmlns:XMLSchema-instance="`+schemaInstance+`" XMLSchema-instance:type`, "i:type", -1)

	reqBytes = xmlDoc([]byte(str))
	res, err := sm.entityManager.Put(ctx, sm.getRuleResourceURI(subscriptionName, ruleName), reqBytes)
	defer closeRes(ctx, res)

	if err != nil {
		return nil, err
	}

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	var entry ruleEntry
	err = xml.Unmarshal(b, &entry)
	if err != nil {
		return nil, formatManagementError(b)
	}
	return ruleEntryToEntity(&entry), nil
}

// DeleteRule will delete a rule on the subscription
func (sm *SubscriptionManager) DeleteRule(ctx context.Context, subscriptionName, ruleName string) error {
	ctx, span := sm.startSpanFromContext(ctx, "sb.SubscriptionManager.DeleteRule")
	defer span.End()

	res, err := sm.entityManager.Delete(ctx, sm.getRuleResourceURI(subscriptionName, ruleName))
	defer closeRes(ctx, res)

	return err
}

func ruleEntryToEntity(entry *ruleEntry) *RuleEntity {
	return &RuleEntity{
		RuleDescription: &entry.Content.RuleDescription,
		Entity: &Entity{
			Name: entry.Title,
			ID:   entry.ID,
		},
	}
}

func subscriptionEntryToEntity(entry *subscriptionEntry) *SubscriptionEntity {
	return &SubscriptionEntity{
		SubscriptionDescription: &entry.Content.SubscriptionDescription,
		Entity: &Entity{
			Name: entry.Title,
			ID:   entry.ID,
		},
	}
}

func (sm *SubscriptionManager) getResourceURI(name string) string {
	return "/" + sm.Topic.Name + "/subscriptions/" + name
}

func (sm *SubscriptionManager) getRulesResourceURI(subscriptionName string) string {
	return sm.getResourceURI(subscriptionName) + "/rules"
}

func (sm *SubscriptionManager) getRuleResourceURI(subscriptionName, ruleName string) string {
	return sm.getResourceURI(subscriptionName) + "/rules/" + ruleName
}

// SubscriptionWithBatchedOperations configures the subscription to batch server-side operations.
func SubscriptionWithBatchedOperations() SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		s.EnableBatchedOperations = ptrBool(true)
		return nil
	}
}

// SubscriptionWithForwardDeadLetteredMessagesTo configures the queue to automatically forward dead letter messages to
// the specified target entity.
//
// The ability to forward dead letter messages to a target requires the connection have management authorization. If
// the connection string or Azure Active Directory identity used does not have management authorization, an unauthorized
// error will be returned on the PUT.
func SubscriptionWithForwardDeadLetteredMessagesTo(target Targetable) SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		uri := target.TargetURI()
		s.ForwardDeadLetteredMessagesTo = &uri
		return nil
	}
}

// SubscriptionWithAutoForward configures the queue to automatically forward messages to the specified entity path
//
// The ability to AutoForward to a target requires the connection have management authorization. If the connection
// string or Azure Active Directory identity used does not have management authorization, an unauthorized error will be
// returned on the PUT.
func SubscriptionWithAutoForward(target Targetable) SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		uri := target.TargetURI()
		s.ForwardTo = &uri
		return nil
	}
}

// SubscriptionWithLockDuration configures the subscription to have a duration of a peek-lock; that is, the amount of
// time that the message is locked for other receivers. The maximum value for LockDuration is 5 minutes; the default
// value is 1 minute.
func SubscriptionWithLockDuration(window *time.Duration) SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		if window == nil {
			duration := time.Duration(1 * time.Minute)
			window = &duration
		}
		s.LockDuration = ptrString(durationTo8601Seconds(*window))
		return nil
	}
}

// SubscriptionWithRequiredSessions will ensure the subscription requires senders and receivers to have sessionIDs
func SubscriptionWithRequiredSessions() SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		s.RequiresSession = ptrBool(true)
		return nil
	}
}

// SubscriptionWithDeadLetteringOnMessageExpiration will ensure the Subscription sends expired messages to the dead
// letter queue
func SubscriptionWithDeadLetteringOnMessageExpiration() SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		s.DeadLetteringOnMessageExpiration = ptrBool(true)
		return nil
	}
}

// SubscriptionWithAutoDeleteOnIdle configures the subscription to automatically delete after the specified idle
// interval. The minimum duration is 5 minutes.
func SubscriptionWithAutoDeleteOnIdle(window *time.Duration) SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		if window != nil {
			if window.Minutes() < 5 {
				return errors.New("window must be greater than 5 minutes")
			}
			s.AutoDeleteOnIdle = ptrString(durationTo8601Seconds(*window))
		}
		return nil
	}
}

// SubscriptionWithMessageTimeToLive configures the subscription to set a time to live on messages. This is the duration
// after which the message expires, starting from when the message is sent to Service Bus. This is the default value
// used when TimeToLive is not set on a message itself. If nil, defaults to 14 days.
func SubscriptionWithMessageTimeToLive(window *time.Duration) SubscriptionManagementOption {
	return func(s *SubscriptionDescription) error {
		if window == nil {
			duration := time.Duration(14 * 24 * time.Hour)
			window = &duration
		}
		s.DefaultMessageTimeToLive = ptrString(durationTo8601Seconds(*window))
		return nil
	}
}

func closeRes(ctx context.Context, res *http.Response) {
	if res == nil {
		return
	}

	if err := res.Body.Close(); err != nil {
		tab.For(ctx).Error(err)
	}
}
