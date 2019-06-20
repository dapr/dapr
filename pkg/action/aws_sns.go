package action

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sns"
)

type AWSSns struct {
	Spec EventSourceSpec
}

type AWSSnsMetadata struct {
	TopicArn  string `json:"topicArn"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

type SNSDataPayload struct {
	Message interface{} `json:"message"`
	Subject interface{} `json:"subject"`
}

func NewAWSSns() *AWSSns {
	return &AWSSns{}
}

func (a *AWSSns) Init(eventSourceSpec EventSourceSpec) error {
	a.Spec = eventSourceSpec
	return nil
}

func (a *AWSSns) Write(data interface{}) error {
	b, err := json.Marshal(a.Spec.ConnectionInfo)
	if err != nil {
		return err
	}

	var metadata AWSSnsMetadata
	err = json.Unmarshal(b, &metadata)
	if err != nil {
		return err
	}

	os.Setenv("AWS_ACCESS_KEY_ID", metadata.AccessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", metadata.SecretKey)
	os.Setenv("AWS_REGION", metadata.Region)

	s := session.Must(session.NewSession())
	c := sns.New(s)

	b, err = json.Marshal(data)
	if err != nil {
		return err
	}

	var payload SNSDataPayload
	err = json.Unmarshal(b, &payload)
	if err != nil {
		return err
	}

	msg := fmt.Sprintf("%v", payload.Message)
	subject := fmt.Sprintf("%v", payload.Subject)

	input := &sns.PublishInput{
		Message:  &msg,
		Subject:  &subject,
		TopicArn: &metadata.TopicArn,
	}

	_, err = c.Publish(input)
	if err != nil {
		return err
	}

	return nil
}

func (a *AWSSns) Read(metadata interface{}) (interface{}, error) {
	return nil, nil
}

func (a *AWSSns) ReadAsync(metadata interface{}, callback func([]byte) error) error {
	return nil
}
