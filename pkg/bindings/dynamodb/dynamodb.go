package dynamodb

import (
	"encoding/json"
	"os"

	"github.com/actionscore/actions/pkg/components/bindings"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/google/uuid"
)

//DynamoDB allows performing stateful operations on AWS DynamoDB
type DynamoDB struct {
	client *dynamodb.DynamoDB
	table  string
}

// Metadata is the metadata for DynamoDB
type Metadata struct {
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Table     string `json:"table"`
}

// DynamoItem is a wrapper around a DynamoDB value
type DynamoItem struct {
	ID    string      `json:"id"`
	Value interface{} `json:"value"`
}

// NewDynamoDB returns a new DynamoDB instance
func NewDynamoDB() *DynamoDB {
	return &DynamoDB{}
}

// Init performs connection parsing for DynamoDB
func (d *DynamoDB) Init(metadata bindings.Metadata) error {
	meta, err := d.GetDynamoDBMetadata(metadata)
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

	i := DynamoItem{
		ID:    uuid.New().String(),
		Value: obj,
	}

	item, err := dynamodbattribute.MarshalMap(i)
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

// GetDynamoDBMetadata parses DynamoDB metadata
func (d *DynamoDB) GetDynamoDBMetadata(spec bindings.Metadata) (*Metadata, error) {
	b, err := json.Marshal(spec.Properties)
	if err != nil {
		return nil, err
	}

	var meta Metadata
	err = json.Unmarshal(b, &meta)
	if err != nil {
		return nil, err
	}

	return &meta, nil
}

func (d *DynamoDB) getClient(awsMeta *Metadata) (*dynamodb.DynamoDB, error) {
	os.Setenv("AWS_ACCESS_KEY_ID", awsMeta.AccessKey)
	os.Setenv("AWS_SECRET_ACCESS_KEY", awsMeta.SecretKey)
	os.Setenv("AWS_REGION", awsMeta.Region)

	s := session.Must(session.NewSession())
	c := dynamodb.New(s)

	return c, nil
}
