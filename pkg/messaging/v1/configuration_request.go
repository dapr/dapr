package v1

import (
	"github.com/dapr/components-contrib/configuration"
	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
)

func ToConfigurationGRPCItems(items []*configuration.Item) []*commonv1pb.ConfigurationItem {
	result := make([]*commonv1pb.ConfigurationItem, 0, len(items))

	for _, item := range items {
		result = append(result, ToConfigurationGRPCItem(item))
	}

	return result
}

func ToConfigurationGRPCItem(item *configuration.Item) *commonv1pb.ConfigurationItem {
	return &commonv1pb.ConfigurationItem{
		Key:      item.Key,
		Content:  item.Content,
		Group:    item.Group,
		Label:    item.Label,
		Tags:     item.Tags,
		Metadata: item.Metadata,
	}
}

func FromConfigurationGRPCItems(items []*commonv1pb.ConfigurationItem) []*configuration.Item {
	result := make([]*configuration.Item, 0, len(items))

	for _, item := range items {
		result = append(result, FromConfigurationGRPCItem(item))
	}

	return result
}

func FromConfigurationGRPCItem(item *commonv1pb.ConfigurationItem) *configuration.Item {
	return &configuration.Item{
		Key:      item.Key,
		Content:  item.Content,
		Group:    item.Group,
		Label:    item.Label,
		Tags:     item.Tags,
		Metadata: item.Metadata,
	}
}
