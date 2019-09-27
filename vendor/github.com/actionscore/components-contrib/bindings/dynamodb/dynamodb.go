package dynamodb

import (
	"encoding/json"

	"github.com/aws/aws-sdk-go/aws/credentials"

	"github.com/actionscore/components-contrib/bindings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

//DynamoDB allows performing stateful operations on AWS DynamoDB
type DynamoDB struct {
	client *dynamodb.DynamoDB
	table  string
}

type dynamoDBMetadata struct {
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Table     string `json:"table"`
}

// NewDynamoDB returns a new DynamoDB instance
func NewDynamoDB() *DynamoDB {
	return &DynamoDB{}
}

// Init performs connection parsing for DynamoDB
func (d *DynamoDB) Init(metadata bindings.Metadata) error {
	meta, err := d.getDynamoDBMetadata(metadata)
	if err != nil {
		return err
	}

	client, err := d.getClient(meta)
	if err != nil {
		return err
	}

	d.client = client
	d.table = meta.Table
	return nil
}

func (d *DynamoDB) Write(req *bindings.WriteRequest) error {
	var obj interface{}
	err := json.Unmarshal(req.Data, &obj)
	if err != nil {
		return err
	}

	item, err := dynamodbattribute.MarshalMap(obj)
	if err != nil {
		return err
	}

	input := &dynamodb.PutItemInput{
		Item:      item,
		TableName: aws.String(d.table),
	}

	_, err = d.client.PutItem(input)
	if err != nil {
		return err
	}

	return nil
}

func (d *DynamoDB) getDynamoDBMetadata(spec bindings.Metadata) (*dynamoDBMetadata, error) {
	b, err := json.Marshal(spec.Properties)
	if err != nil {
		return nil, err
	}

	var meta dynamoDBMetadata
	err = json.Unmarshal(b, &meta)
	if err != nil {
		return nil, err
	}
	return &meta, nil
}

func (d *DynamoDB) getClient(meta *dynamoDBMetadata) (*dynamodb.DynamoDB, error) {
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(meta.Region),
		Credentials: credentials.NewStaticCredentials(meta.AccessKey, meta.SecretKey, ""),
	})
	if err != nil {
		return nil, err
	}

	c := dynamodb.New(sess)
	return c, nil
}
