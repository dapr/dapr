package action

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type MQTT struct {
	Metadata *MQTTMetadata
	Client   mqtt.Client
}

type MQTTMetadata struct {
	URL   string `json:"url"`
	Topic string `json:"topic"`
}

func NewMQTT() *MQTT {
	return &MQTT{}
}

func (m *MQTT) Init(eventSourceSpec EventSourceSpec) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	metadata, err := m.GetMQTTMetadata(eventSourceSpec)
	if err != nil {
		return err
	}

	m.Metadata = metadata

	if m.Metadata.URL == "" {
		return errors.New("MQTT Error: URL required")
	}

	if m.Metadata.Topic == "" {
		return errors.New("MQTT error: topic required")
	}

	return nil
}

func (m *MQTT) GetMQTTMetadata(eventSourceSpec EventSourceSpec) (*MQTTMetadata, error) {
	b, err := json.Marshal(eventSourceSpec.ConnectionInfo)
	if err != nil {
		return nil, err
	}

	var mqttMetadata MQTTMetadata
	err = json.Unmarshal(b, &mqttMetadata)
	if err != nil {
		return nil, err
	}

	return &mqttMetadata, nil
}

func (m *MQTT) Write(data interface{}) error {
	uri, err := url.Parse(m.Metadata.URL)
	if err != nil {
		return err
	}

	client, err := m.connect("pub", uri)
	if err != nil {
		return err
	}

	client.Publish(m.Metadata.Topic, 0, false, fmt.Sprintf("%s", data))
	client.Disconnect(0)

	return nil
}

func (m *MQTT) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (m *MQTT) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	uri, err := url.Parse(m.Metadata.URL)
	if err != nil {
		return err
	}

	client, err := m.connect("sub", uri)
	if err != nil {
		return err
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	client.Subscribe(m.Metadata.Topic, 0, func(client mqtt.Client, msg mqtt.Message) {
		if len(msg.Payload()) > 0 {
			callback(msg.Payload())
		}
	})

	<-c
	return nil
}

func (m *MQTT) connect(clientID string, uri *url.URL) (mqtt.Client, error) {
	opts := m.createClientOptions(clientID, uri)
	client := mqtt.NewClient(opts)
	token := client.Connect()
	for !token.WaitTimeout(3 * time.Second) {
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	return client, nil
}

func (m *MQTT) createClientOptions(clientID string, uri *url.URL) *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tcp://%s", uri.Host))
	opts.SetUsername(uri.User.Username())
	password, _ := uri.User.Password()
	opts.SetPassword(password)
	opts.SetClientID(clientID)
	return opts
}
