package sns

import (
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"

	"github.com/actionscore/components-contrib/bindings"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sns"
)

// AWSSNS is an AWS SNS binding
type AWSSNS struct {
	client   *sns.SNS
	topicARN string
}

type snsMetadata struct {
	TopicArn  string `json:"topicArn"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

type dataPayload struct {
	Message interface{} `json:"message"`
	Subject interface{} `json:"subject"`
}

// NewAWSSNS creates a new AWSSNS binding instance
func NewAWSSNS() *AWSSNS {
	return &AWSSNS{}
}

// Init does metadata parsing
func (a *AWSSNS) Init(metadata bindings.Metadata) error {
	m, err := a.parseMetadata(metadata)
	if err != nil {
		return err
	}
	client, err := a.getClient(m)
	if err != nil {
		return err
	}
	a.client = client
	a.topicARN = m.TopicArn
	return nil
}

func (a *AWSSNS) parseMetadata(metadata bindings.Metadata) (*snsMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var m snsMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *AWSSNS) getClient(metadata *snsMetadata) (*sns.SNS, error) {
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(metadata.Region),
		Credentials: credentials.NewStaticCredentials(metadata.AccessKey, metadata.SecretKey, ""),
	})
	if err != nil {
		return nil, err
	}
	c := sns.New(sess)
	return c, nil
}

func (a *AWSSNS) Write(req *bindings.WriteRequest) error {
	var payload dataPayload
	err := json.Unmarshal(req.Data, &payload)
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("%v", payload.Message)
	subject := fmt.Sprintf("%v", payload.Subject)

	input := &sns.PublishInput{
		Message:  &msg,
		Subject:  &subject,
		TopicArn: &a.topicARN,
	}

	_, err = a.client.Publish(input)
	if err != nil {
		return err
	}
	return nil
}
