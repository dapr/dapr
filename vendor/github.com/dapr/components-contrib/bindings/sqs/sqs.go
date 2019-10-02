package sqs

import (
	"encoding/json"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"

	log "github.com/Sirupsen/logrus"
	"github.com/dapr/components-contrib/bindings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/sqs"
)

// AWSSQS allows receiving and sending data to/from AWS SQS
type AWSSQS struct {
	Client   *sqs.SQS
	QueueURL *string
}

type sqsMetadata struct {
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
	m, err := a.parseSQSMetadata(metadata)
	if err != nil {
		return err
	}

	client, err := a.getClient(m)
	if err != nil {
		return err
	}

	queueName := m.QueueName
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
	msgBody := string(req.Data)
	_, err := a.Client.SendMessage(&sqs.SendMessageInput{
		MessageBody: &msgBody,
		QueueUrl:    a.QueueURL,
	})
	return err
}

func (a *AWSSQS) Read(handler func(*bindings.ReadResponse) error) error {
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

func (a *AWSSQS) parseSQSMetadata(metadata bindings.Metadata) (*sqsMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var m sqsMetadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (a *AWSSQS) getClient(metadata *sqsMetadata) (*sqs.SQS, error) {
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(metadata.Region),
		Credentials: credentials.NewStaticCredentials(metadata.AccessKey, metadata.SecretKey, ""),
	})
	if err != nil {
		return nil, err
	}

	c := sqs.New(sess)
	return c, nil
}
