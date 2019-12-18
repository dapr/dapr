// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package s3

import (
	"bytes"
	"encoding/json"

	log "github.com/sirupsen/logrus"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/google/uuid"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/dapr/components-contrib/bindings"
)

// AWSS3 is a binding for an AWS S3 storage bucket
type AWSS3 struct {
	metadata *s3Metadata
	uploader *s3manager.Uploader
}

type s3Metadata struct {
	Region    string `json:"region"`
	AccessKey string `json:"accessKey"`
	SecretKey string `json:"secretKey"`
	Bucket    string `json:"bucket"`
}

// NewAWSS3 returns a new AWSS3 instance
func NewAWSS3() *AWSS3 {
	return &AWSS3{}
}

// Init does metadata parsing and connection creation
func (s *AWSS3) Init(metadata bindings.Metadata) error {
	m, err := s.parseMetadata(metadata)
	if err != nil {
		return err
	}
	uploader, err := s.getClient(m)
	if err != nil {
		return err
	}
	s.metadata = m
	s.uploader = uploader
	return nil
}

func (s *AWSS3) Write(req *bindings.WriteRequest) error {
	key := ""
	if val, ok := req.Metadata["key"]; ok && val != "" {
		key = val
	} else {
		key = uuid.New().String()
		log.Debugf("key not found. generating key %s", key)
	}

	r := bytes.NewReader(req.Data)
	_, err := s.uploader.Upload(&s3manager.UploadInput{
		Bucket: aws.String(s.metadata.Bucket),
		Key:    aws.String(key),
		Body:   r,
	})
	return err
}

func (s *AWSS3) parseMetadata(metadata bindings.Metadata) (*s3Metadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var m s3Metadata
	err = json.Unmarshal(b, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (s *AWSS3) getClient(metadata *s3Metadata) (*s3manager.Uploader, error) {
	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(metadata.Region),
		Credentials: credentials.NewStaticCredentials(metadata.AccessKey, metadata.SecretKey, ""),
	})
	if err != nil {
		return nil, err
	}

	uploader := s3manager.NewUploader(sess)
	return uploader, nil
}
