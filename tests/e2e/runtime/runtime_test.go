// +build e2e

// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------
package runtime_e2e

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	"github.com/coreos/go-iptables/iptables"
	"github.com/dapr/dapr/tests/e2e/utils"
	kube "github.com/dapr/dapr/tests/platforms/kubernetes"
	"github.com/dapr/dapr/tests/runner"
	"github.com/go-redis/redis/v7"
	"github.com/stretchr/testify/require"
)

const numHealthChecks = 60 // Number of get calls before starting tests.

var tr *runner.TestRunner

const (
	runtimeAppName   = "runtime"
	numRedisMessages = 10
	numKafkaMessages = 15
)

type appResponse struct {
	Message   string `json:"message,omitempty"`
	StartTime int    `json:"start_time,omitempty"`
	EndTime   int    `json:"end_time,omitempty"`
}

type daprAPIResponse struct {
	DaprHTTPSuccess int `json:"dapr_http_success"`
	DaprHTTPError   int `json:"dapr_http_error"`
	// TODO: gRPC API
}

var kafkaPort int

func getAPIResponse(t *testing.T, testName, runtimeExternalURL string) (*daprAPIResponse, error) {
	// this is the publish app's endpoint, not a dapr endpoint
	url := fmt.Sprintf("http://%s/tests/%s", runtimeExternalURL, testName)

	resp, err := http.Get(url)
	defer resp.Body.Close()

	require.NoError(t, err)
	require.Equal(t, resp.StatusCode, http.StatusOK)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var appResp daprAPIResponse
	err = json.Unmarshal(body, &appResp)
	require.NoError(t, err)

	return &appResp, nil
}

func publishMessagesToRedis(addr, password, topic string, numMessages int) error {
	opts := &redis.Options{
		Addr:            addr,
		Password:        password,
		DB:              0,
		MaxRetries:      3,
		MaxRetryBackoff: time.Second * 2,
	}

	client := redis.NewClient(opts)

	log.Println("Sending messages to redis")
	for i := 0; i < numMessages; i++ {
		_, err := client.XAdd(&redis.XAddArgs{
			Stream: topic,
			Values: map[string]interface{}{"data": fmt.Sprintf("message%d", i)},
		}).Result()
		if err != nil {
			return fmt.Errorf("redis streams: error from publish: %s", err)
		}
	}

	return nil
}

func initRedis(platform runner.PlatformInterface) error {
	log.Println("Forwarding to redis")

	forwarder := platform.GetPortForwarder()
	localPorts, err := forwarder.Connect("dapr-redis-master-0", 6379)
	if err != nil {
		return fmt.Errorf("Failed to establish tunnel to redis: %+v", err)
	}
	defer forwarder.Close()
	redisPort := localPorts[0]

	// Publish messages on to a topic
	topic := "runtime-pubsub-http"
	redisAddr := fmt.Sprintf("localhost:%d", redisPort)
	redisPass := ""
	log.Printf("Redis available on %s", redisAddr)

	err = publishMessagesToRedis(redisAddr, redisPass, topic, numRedisMessages)
	if err != nil {
		return fmt.Errorf("Failed to publish messages: %+v", err)
	}

	return nil
}

func publishMessagesToKafka(brokers []string, topic string, numMessages int) error {

	sarama.Logger = log.New(os.Stdout, "[sarama] ", log.LstdFlags)

	config := sarama.NewConfig()
	config.Producer.RequiredAcks = sarama.WaitForAll
	config.Producer.Retry.Max = 5
	config.Producer.Return.Successes = true
	config.Producer.Return.Errors = true
	config.Version = sarama.V1_0_0_0

	c, err := sarama.NewClient(brokers, config)
	if err != nil {
		return err
	}

	b := c.Brokers()[0] // We only expect one broker
	bAddr := strings.Split(b.Addr(), ":")[0]

	// TODO: Handle on Windows and without sudo?!
	log.Printf("Adding DNAT rule: %s:9092-> 127.0.0.1:%d\n", bAddr, kafkaPort)
	iptable, err := iptables.New()
	if err != nil {
		return err
	}
	err = iptable.Append("nat", "OUTPUT", "-d", bAddr, "-j", "DNAT", "-p", "tcp", "--dport", "9092", "--to-destination", fmt.Sprintf("127.0.0.1:%d", kafkaPort))
	if err != nil {
		return err
	}

	p, err := sarama.NewSyncProducerFromClient(c)
	if err != nil {
		return err
	}
	defer func() {
		p.Close()

		log.Printf("Removing DNAT rule: %s:9092 -> 127.0.0.1:%d\n", bAddr, kafkaPort)
		iptable.Delete("nat", "OUTPUT", "-d", bAddr, "-j", "DNAT", "-p", "tcp", "--dport", "9092", "--to-destination", fmt.Sprintf("127.0.0.1:%d", kafkaPort))
	}()

	log.Println("Sending messages to kafka")
	for i := 0; i < numMessages; i++ {
		msg := &sarama.ProducerMessage{
			Topic: topic,
			Value: sarama.ByteEncoder(fmt.Sprintf("message%d", i)),
		}
		log.Printf("%d\n", i)
		_, _, err = p.SendMessage(msg)
		if err != nil {
			return err
		}
	}

	return nil
}

func initKafka(platform runner.PlatformInterface) error {
	log.Println("Forwarding to kafka")

	forwarder := platform.GetPortForwarder()
	localPorts, err := forwarder.Connect("dapr-kafka-0", 9092)
	if err != nil {
		return fmt.Errorf("Failed to establish tunnel to kafka: %+v", err)
	}
	defer forwarder.Close()
	kafkaPort = localPorts[0]

	// Publish messages on to a topic
	topic := "runtime-bindings-http"
	addr := fmt.Sprintf("localhost:%d", kafkaPort)
	brokers := []string{
		addr,
	}
	log.Printf("Kafka available on %s", addr)

	err = publishMessagesToKafka(brokers, topic, numKafkaMessages)
	if err != nil {
		return fmt.Errorf("Failed to publish messages: %+v", err)
	}

	return nil
}

func TestMain(m *testing.M) {
	fmt.Println("Enter TestMain")
	// These apps will be deployed before starting actual test
	// and will be cleaned up after all tests are finished automatically
	testApps := []kube.AppDescription{
		{
			AppName:        runtimeAppName,
			DaprEnabled:    true,
			ImageName:      "e2e-runtime",
			Replicas:       1,
			IngressEnabled: true,
		},
	}

	// Initialise components before deploying
	// the apps and initializing the dapr sidecars
	preDeploy := func(platform runner.PlatformInterface) error {
		err := initRedis(platform)
		if err != nil {
			log.Fatalf("initRedis error: %+v", err)
		}

		err = initKafka(platform)
		if err != nil {
			log.Fatalf("initKafka error: %+v", err)
		}

		return nil
	}

	log.Printf("Creating TestRunner\n")
	tr = runner.NewTestRunner("runtimetest", testApps, nil, preDeploy, nil)
	log.Printf("Starting TestRunner\n")
	os.Exit(tr.Start(m))
}

func TestRuntimeInitPubsub(t *testing.T) {
	t.Log("Enter TestRuntimeInitPubsub")

	// Get subscriber app URL
	runtimeExternalURL := tr.Platform.AcquireAppExternalURL(runtimeAppName)
	require.NotEmpty(t, runtimeExternalURL, "runtimeExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(runtimeExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Get API responses from subscriber
	apiResponse, err := getAPIResponse(t, "pubsub", runtimeExternalURL)
	require.NoError(t, err)

	// Assert
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, numRedisMessages, apiResponse.DaprHTTPSuccess)
}

func TestRuntimeInitBindings(t *testing.T) {
	t.Log("Enter TestRuntimeInitBindings")

	// Get subscriber app URL
	runtimeExternalURL := tr.Platform.AcquireAppExternalURL(runtimeAppName)
	require.NotEmpty(t, runtimeExternalURL, "runtimeExternalURL must not be empty!")

	// This initial probe makes the test wait a little bit longer when needed,
	// making this test less flaky due to delays in the deployment.
	_, err := utils.HTTPGetNTimes(runtimeExternalURL, numHealthChecks)
	require.NoError(t, err)

	// Get API responses from subscriber
	apiResponse, err := getAPIResponse(t, "bindings", runtimeExternalURL)
	require.NoError(t, err)

	// Assert
	require.Equal(t, 0, apiResponse.DaprHTTPError)
	require.Equal(t, numKafkaMessages, apiResponse.DaprHTTPSuccess)
}
