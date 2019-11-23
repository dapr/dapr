// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package servicebus

import (
	"context"
	"fmt"
	"strconv"
	"time"

	azservicebus "github.com/Azure/azure-service-bus-go"
	log "github.com/Sirupsen/logrus"
	"github.com/dapr/components-contrib/pubsub"
)

const (
	// Keys
	connectionString              = "connectionString"
	consumerID                    = "consumerID"
	maxDeliveryCount              = "maxDeliveryCount"
	timeoutInSec                  = "timeoutInSec"
	lockDurationInSec             = "lockDurationInSec"
	defaultMessageTimeToLiveInSec = "defaultMessageTimeToLiveInSec"
	autoDeleteOnIdleInSec         = "autoDeleteOnIdleInSec"
	disableEntityManagement       = "disableEntityManagement"
	errorMessagePrefix            = "azure service bus error:"

	// Defaults
	defaultTimeoutInSec            = 60
	defaultDisableEntityManagement = false
)

type azureServiceBus struct {
	metadata     metadata
	namespace    *azservicebus.Namespace
	topicManager *azservicebus.TopicManager
}

type subscription interface {
	Close(ctx context.Context) error
	Receive(ctx context.Context, handler azservicebus.Handler) error
}

// NewAzureServiceBus returns a new Azure ServiceBus pub-sub implementation
func NewAzureServiceBus() pubsub.PubSub {
	return &azureServiceBus{}
}

func parseAzureServiceBusMetadata(meta pubsub.Metadata) (metadata, error) {
	m := metadata{}

	/* Required configuration settings - no defaults */
	if val, ok := meta.Properties[connectionString]; ok && val != "" {
		m.ConnectionString = val
	} else {
		return m, fmt.Errorf("%s missing connection string", errorMessagePrefix)
	}

	if val, ok := meta.Properties[consumerID]; ok && val != "" {
		m.ConsumerID = val
	} else {
		return m, fmt.Errorf("%s missing consumerID", errorMessagePrefix)
	}

	/* Optional configuration settings - defaults will be set by the client */
	m.TimeoutInSec = defaultTimeoutInSec
	if val, ok := meta.Properties[timeoutInSec]; ok && val != "" {
		var err error
		m.TimeoutInSec, err = strconv.Atoi(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid timeoutInSec %s, %s", errorMessagePrefix, val, err)
		}
	}

	m.DisableEntityManagement = defaultDisableEntityManagement
	if val, ok := meta.Properties[disableEntityManagement]; ok && val != "" {
		var err error
		m.DisableEntityManagement, err = strconv.ParseBool(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid disableEntityManagement %s, %s", errorMessagePrefix, val, err)
		}
	}

	/* Nullable configuration settings - defaults will be set by the server */
	if val, ok := meta.Properties[maxDeliveryCount]; ok && val != "" {
		valAsInt, err := strconv.Atoi(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid maxDeliveryCount %s, %s", errorMessagePrefix, val, err)
		}
		m.MaxDeliveryCount = &valAsInt
	}

	if val, ok := meta.Properties[lockDurationInSec]; ok && val != "" {
		valAsInt, err := strconv.Atoi(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid lockDurationInSec %s, %s", errorMessagePrefix, val, err)
		}
		m.LockDurationInSec = &valAsInt
	}

	if val, ok := meta.Properties[defaultMessageTimeToLiveInSec]; ok && val != "" {
		valAsInt, err := strconv.Atoi(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid defaultMessageTimeToLiveInSec %s, %s", errorMessagePrefix, val, err)
		}
		m.DefaultMessageTimeToLiveInSec = &valAsInt
	}

	if val, ok := meta.Properties[autoDeleteOnIdleInSec]; ok && val != "" {
		valAsInt, err := strconv.Atoi(val)
		if err != nil {
			return m, fmt.Errorf("%s invalid autoDeleteOnIdleInSecKey %s, %s", errorMessagePrefix, val, err)
		}
		m.AutoDeleteOnIdleInSec = &valAsInt
	}

	return m, nil
}

func (a *azureServiceBus) Init(metadata pubsub.Metadata) error {
	m, err := parseAzureServiceBusMetadata(metadata)
	if err != nil {
		return err
	}

	a.metadata = m
	a.namespace, err = azservicebus.NewNamespace(azservicebus.NamespaceWithConnectionString(a.metadata.ConnectionString))
	if err != nil {
		return err
	}

	a.topicManager = a.namespace.NewTopicManager()
	return nil
}

func (a *azureServiceBus) Publish(req *pubsub.PublishRequest) error {
	if !a.metadata.DisableEntityManagement {
		err := a.ensureTopic(req.Topic)
		if err != nil {
			return err
		}
	}

	sender, err := a.namespace.NewTopic(req.Topic)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(a.metadata.TimeoutInSec))
	defer cancel()

	err = sender.Send(ctx, azservicebus.NewMessage(req.Data))
	if err != nil {
		return err
	}
	return nil
}

func (a *azureServiceBus) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	subID := a.metadata.ConsumerID
	if !a.metadata.DisableEntityManagement {
		err := a.ensureSubscription(subID, req.Topic)
		if err != nil {
			return err
		}
	}
	topic, err := a.namespace.NewTopic(req.Topic)
	if err != nil {
		return fmt.Errorf("%s could not instantiate topic %s, %s", errorMessagePrefix, req.Topic, err)
	}

	var sub subscription
	sub, err = topic.NewSubscription(subID)
	if err != nil {
		return fmt.Errorf("%s could not instantiate subscription %s for topic %s", errorMessagePrefix, subID, req.Topic)
	}

	sbHandlerFunc := azservicebus.HandlerFunc(a.getHandlerFunc(req.Topic, handler))

	ctx := context.Background()
	go a.handleSubscriptionMessages(ctx, req.Topic, sub, sbHandlerFunc)

	return nil
}

func (a *azureServiceBus) getHandlerFunc(topic string, handler func(msg *pubsub.NewMessage) error) func(ctx context.Context, message *azservicebus.Message) error {
	return func(ctx context.Context, message *azservicebus.Message) error {
		msg := &pubsub.NewMessage{
			Data:  message.Data,
			Topic: topic,
		}
		err := handler(msg)
		if err != nil {
			return message.Abandon(ctx)
		}
		return message.Complete(ctx)
	}
}

func (a *azureServiceBus) handleSubscriptionMessages(ctx context.Context, topic string, sub subscription, handlerFunc azservicebus.HandlerFunc) {
	for {
		if err := sub.Receive(ctx, handlerFunc); err != nil {
			log.Errorf("%s error receiving from topic %s, %s", errorMessagePrefix, topic, err)
			return
		}
	}
}

func (a *azureServiceBus) ensureTopic(topic string) error {
	entity, err := a.getTopicEntity(topic)
	if err != nil {
		return err
	}

	if entity == nil {
		err = a.createTopicEntity(topic)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *azureServiceBus) ensureSubscription(name string, topic string) error {
	err := a.ensureTopic(topic)
	if err != nil {
		return err
	}

	subManager, err := a.namespace.NewSubscriptionManager(topic)
	if err != nil {
		return err
	}

	entity, err := a.getSubscriptionEntity(subManager, topic, name)
	if err != nil {
		return err
	}

	if entity == nil {
		err = a.createSubscriptionEntity(subManager, topic, name)
		if err != nil {
			return err
		}
	}
	return nil
}

func (a *azureServiceBus) getTopicEntity(topic string) (*azservicebus.TopicEntity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(a.metadata.TimeoutInSec))
	defer cancel()

	if a.topicManager == nil {
		return nil, fmt.Errorf("%s init() has not been called", errorMessagePrefix)
	}
	topicEntity, err := a.topicManager.Get(ctx, topic)
	if err != nil && !azservicebus.IsErrNotFound(err) {
		return nil, fmt.Errorf("%s could not get topic %s, %s", errorMessagePrefix, topic, err)
	}
	return topicEntity, nil
}

func (a *azureServiceBus) createTopicEntity(topic string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(a.metadata.TimeoutInSec))
	defer cancel()
	_, err := a.topicManager.Put(ctx, topic)
	if err != nil {
		return fmt.Errorf("%s could not put topic %s, %s", errorMessagePrefix, topic, err)
	}
	return nil
}

func (a *azureServiceBus) getSubscriptionEntity(mgr *azservicebus.SubscriptionManager, topic, subscription string) (*azservicebus.SubscriptionEntity, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(a.metadata.TimeoutInSec))
	defer cancel()
	entity, err := mgr.Get(ctx, subscription)
	if err != nil && !azservicebus.IsErrNotFound(err) {
		return nil, fmt.Errorf("%s could not get subscription %s, %s", errorMessagePrefix, subscription, err)
	}
	return entity, nil
}

func (a *azureServiceBus) createSubscriptionEntity(mgr *azservicebus.SubscriptionManager, topic, subscription string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*time.Duration(a.metadata.TimeoutInSec))
	defer cancel()

	opts, err := a.createSubscriptionManagementOptions()
	if err != nil {
		return err
	}

	_, err = mgr.Put(ctx, subscription, opts...)
	if err != nil {
		return fmt.Errorf("%s could not put subscription %s, %s", errorMessagePrefix, subscription, err)
	}
	return nil
}

func (a *azureServiceBus) createSubscriptionManagementOptions() ([]azservicebus.SubscriptionManagementOption, error) {
	var opts []azservicebus.SubscriptionManagementOption
	if a.metadata.MaxDeliveryCount != nil {
		opts = append(opts, subscriptionManagementOptionsWithMaxDeliveryCount(a.metadata.MaxDeliveryCount))
	}
	if a.metadata.LockDurationInSec != nil {
		opts = append(opts, subscriptionManagementOptionsWithLockDuration(a.metadata.LockDurationInSec))
	}
	if a.metadata.DefaultMessageTimeToLiveInSec != nil {
		opts = append(opts, subscriptionManagementOptionsWithDefaultMessageTimeToLive(a.metadata.DefaultMessageTimeToLiveInSec))
	}
	if a.metadata.AutoDeleteOnIdleInSec != nil {
		opts = append(opts, subscriptionManagementOptionsWithAutoDeleteOnIdle(a.metadata.AutoDeleteOnIdleInSec))
	}
	return opts, nil
}

func subscriptionManagementOptionsWithMaxDeliveryCount(maxDeliveryCount *int) azservicebus.SubscriptionManagementOption {
	return func(d *azservicebus.SubscriptionDescription) error {
		mdc := int32(*maxDeliveryCount)
		d.MaxDeliveryCount = &mdc
		return nil
	}
}

func subscriptionManagementOptionsWithAutoDeleteOnIdle(durationInSec *int) azservicebus.SubscriptionManagementOption {
	return func(d *azservicebus.SubscriptionDescription) error {
		duration := fmt.Sprintf("PT%dS", *durationInSec)
		d.AutoDeleteOnIdle = &duration
		return nil
	}
}

func subscriptionManagementOptionsWithDefaultMessageTimeToLive(durationInSec *int) azservicebus.SubscriptionManagementOption {
	return func(d *azservicebus.SubscriptionDescription) error {
		duration := fmt.Sprintf("PT%dS", *durationInSec)
		d.DefaultMessageTimeToLive = &duration
		return nil
	}
}

func subscriptionManagementOptionsWithLockDuration(durationInSec *int) azservicebus.SubscriptionManagementOption {
	return func(d *azservicebus.SubscriptionDescription) error {
		duration := fmt.Sprintf("PT%dS", *durationInSec)
		d.LockDuration = &duration
		return nil
	}
}
