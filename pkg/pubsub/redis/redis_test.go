package redis

import (
	"errors"
	"fmt"
	"testing"

	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"

	"github.com/actionscore/actions/pkg/components/pubsub"
)

func getFakeProperties() map[string]string {
	return map[string]string{
		consumerID: "fakeConsumer",
		host:       "fake.redis.com",
		password:   "fakePassword",
	}
}

func TestParseRedisMetadata(t *testing.T) {
	t.Run("metadata is correct", func(t *testing.T) {
		fakeProperties := getFakeProperties()

		fakeMetaData := pubsub.Metadata{
			Properties: fakeProperties,
		}

		// act
		m, err := parseRedisMetadata(fakeMetaData)

		// assert
		assert.NoError(t, err)
		assert.Equal(t, fakeProperties[host], m.host)
		assert.Equal(t, fakeProperties[password], m.password)
		assert.Equal(t, fakeProperties[consumerID], m.consumerID)
	})

	t.Run("host is not given", func(t *testing.T) {
		fakeProperties := getFakeProperties()

		fakeMetaData := pubsub.Metadata{
			Properties: fakeProperties,
		}
		fakeMetaData.Properties[host] = ""

		// act
		m, err := parseRedisMetadata(fakeMetaData)

		// assert
		assert.Error(t, errors.New("redis streams error: missing host address"), err)
		assert.Empty(t, m.host)
		assert.Empty(t, m.password)
		assert.Empty(t, m.consumerID)
	})

	t.Run("consumerID is not given", func(t *testing.T) {
		fakeProperties := getFakeProperties()

		fakeMetaData := pubsub.Metadata{
			Properties: fakeProperties,
		}
		fakeMetaData.Properties[consumerID] = ""

		// act
		m, err := parseRedisMetadata(fakeMetaData)
		// assert
		assert.Error(t, errors.New("redis streams error: missing consumerID"), err)
		assert.Equal(t, fakeProperties[host], m.host)
		assert.Equal(t, fakeProperties[password], m.password)
		assert.Empty(t, m.consumerID)
	})
}

func TestProcessStreams(t *testing.T) {
	fakeConsumerID := "fakeConsumer"
	topicCount := 0
	messageCount := 0

	fakeHandler := func(msg *pubsub.NewMessage) error {
		expectedTopic := fmt.Sprintf("Topic%d", topicCount)
		expectedData := fmt.Sprintf("testData%d", messageCount)

		messageCount++
		if topicCount == 0 && messageCount >= 3 {
			topicCount = 1
			messageCount = 0
		}

		// assert
		assert.Equal(t, expectedTopic, msg.Topic)
		assert.Equal(t, expectedData, string(msg.Data))

		// return fake error to skip executing redis client command
		return errors.New("fake error")
	}

	// act
	testRedisStream := &redisStreams{}
	testRedisStream.processStreams(fakeConsumerID, generateRedisStreamTestData(2, 3), fakeHandler)

	// assert
	assert.Equal(t, 1, topicCount)
	assert.Equal(t, 3, messageCount)
}

func generateRedisStreamTestData(topicCount, messageCount int) []redis.XStream {
	generateXMessage := func(id int) redis.XMessage {
		return redis.XMessage{
			ID: fmt.Sprintf("%d", id),
			Values: map[string]interface{}{
				"data": fmt.Sprintf("testData%d", id),
			},
		}
	}

	xmessageArray := make([]redis.XMessage, messageCount)
	for i := range xmessageArray {
		xmessageArray[i] = generateXMessage(i)
	}

	redisStreams := make([]redis.XStream, topicCount)
	for i := range redisStreams {
		redisStreams[i] = redis.XStream{
			Stream:   fmt.Sprintf("Topic%d", i),
			Messages: xmessageArray,
		}
	}
	return redisStreams
}
