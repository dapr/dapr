package mqtt

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

	"github.com/actionscore/actions/pkg/components/bindings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTT allows sending and receving data to/from an MQTT broker
type MQTT struct {
	Metadata *Metadata
	Client   mqtt.Client
}

// Metadata is the MQTT config
type Metadata struct {
	URL   string `json:"url"`
	Topic string `json:"topic"`
}

// NewMQTT returns a new MQTT instance
func NewMQTT() *MQTT {
	return &MQTT{}
}

// Init does MQTT connection parsing
func (m *MQTT) Init(metadata bindings.Metadata) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	mqttMeta, err := m.GetMQTTMetadata(metadata)
	if err != nil {
		return err
	}

	m.Metadata = mqttMeta

	if m.Metadata.URL == "" {
		return errors.New("MQTT Error: URL required")
	}

	if m.Metadata.Topic == "" {
		return errors.New("MQTT error: topic required")
	}

	return nil
}

// GetMQTTMetadata returns new MQTT metadata
func (m *MQTT) GetMQTTMetadata(metadata bindings.Metadata) (*Metadata, error) {
	b, err := json.Marshal(metadata.ConnectionInfo)
	if err != nil {
		return nil, err
	}

	var mqttMetadata Metadata
	err = json.Unmarshal(b, &mqttMetadata)
	if err != nil {
		return nil, err
	}

	return &mqttMetadata, nil
}

func (m *MQTT) Write(req *bindings.WriteRequest) error {
	uri, err := url.Parse(m.Metadata.URL)
	if err != nil {
		return err
	}

	client, err := m.connect("pub", uri)
	if err != nil {
		return err
	}

	client.Publish(m.Metadata.Topic, 0, false, string(req.Data))
	client.Disconnect(0)

	return nil
}

func (m *MQTT) Read(handler func(*bindings.ReadResponse) error) error {
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
			handler(&bindings.ReadResponse{
				Data: msg.Payload(),
			})
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
