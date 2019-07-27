package sqs

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"os"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
)

// AWSSQS allows receiving and sending data to/from AWS SQS
type AWSSQS struct {
	Spec     bindings.Metadata
	Client   *sqs.SQS
	QueueURL *string
}

// Metadata is the config for AWS SQS
type Metadata struct {
	QueueName string `json:"queueName"`
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
}

// NewAWSSQS returns a new AWS SQS instance
func NewAWSSQS() *AWSSQS {
	return &AWSSQS{}
}

// Init does metadata parsing and connection creation
func (a *AWSSQS) Init(metadata bindings.Metadata) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	a.Spec = metadata

	awsMeta, err := a.GetAWSMetadata()
	if err != nil {
		return err
	}

	client, err := a.getClient(awsMeta)
	if err != nil {
		return err
	}

	queueName := awsMeta.QueueName

	resultURL, err := client.GetQueueUrl(&sqs.GetQueueUrlInput{
		QueueName: aws.String(queueName),
	})
	if err != nil {
		return err
	}

	a.QueueURL = resultURL.QueueUrl
	a.Client = client

	return nil
}

func (a *AWSSQS) Write(req *bindings.WriteRequest) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	msgBody := string(req.Data)
	_, err := a.Client.SendMessage(&sqs.SendMessageInput{
		MessageBody: &msgBody,
		QueueUrl:    a.QueueURL,
	})
	return err
}

func (a *AWSSQS) Read(handler func(*bindings.ReadResponse) error) error {
	http.DefaultTransport.(*http.Transport).TLSClientConfig = &tls.Config{InsecureSkipVerify: true}

	for {
		result, err := a.Client.ReceiveMessage(&sqs.ReceiveMessageInput{
			QueueUrl: a.QueueURL,
			AttributeNames: aws.StringSlice([]string{
				"SentTimestamp",
			}),
			MaxNumberOfMessages: aws.Int64(1),
			MessageAttributeNames: aws.StringSlice([]string{
				"All",
			}),
			WaitTimeSeconds: aws.Int64(20),
		})
		if err != nil {
			log.Errorf("Unable to receive message from queue %q, %v.", *a.QueueURL, err)
		}

		if len(result.Messages) > 0 {
			for _, m := range result.Messages {
				body := m.Body
				res := bindings.ReadResponse{
					Data: []byte(*body),
				}
				err := handler(&res)
				if err == nil {
					msgHandle := m.ReceiptHandle

					a.Client.DeleteMessage(&sqs.DeleteMessageInput{
						QueueUrl:      a.QueueURL,
						ReceiptHandle: msgHandle,
					})
				}
			}
		}

		time.Sleep(time.Millisecond * 50)
	}
}

// GetAWSMetadata gets AWS metadata
func (a *AWSSQS) GetAWSMetadata() (*Metadata, error) {
	b, err := json.Marshal(a.Spec.ConnectionInfo)
	if err != nil {
		return nil, err
	}

	var awsMeta Metadata
	err = json.Unmarshal(b, &awsMeta)
	if err != nil {
		return nil, err
	}

	return &awsMeta, nil
}

func (a *AWSSQS) getClient(awsMeta *Metadata) (*sqs.SQS, error) {
	os.Setenv("AWS_ACCESS_KEY_ID", awsMeta.AccessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", awsMeta.SecretKey)
	os.Setenv("AWS_REGION", awsMeta.Region)

	s := session.Must(session.NewSession())
	c := sqs.New(s)

	return c, nil
}
