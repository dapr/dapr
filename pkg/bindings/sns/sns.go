package sns

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sns"
)

type AWSSns struct {
	Spec bindings.Metadata
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

func (a *AWSSns) Init(metadata bindings.Metadata) error {
	a.Spec = metadata
	return nil
}

func (a *AWSSns) Write(req *bindings.WriteRequest) error {
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

	var payload SNSDataPayload
	err = json.Unmarshal(req.Data, &payload)
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
