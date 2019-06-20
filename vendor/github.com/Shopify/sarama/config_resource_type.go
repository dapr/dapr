package sarama

//ConfigResourceType is a type for config resource
type ConfigResourceType int8

// Taken from :
// https://cwiki.apache.org/confluence/display/KAFKA/KIP-133%3A+Describe+and+Alter+Configs+Admin+APIs#KIP-133:DescribeandAlterConfigsAdminAPIs-WireFormattypes

const (
	//UnknownResource constant type
	UnknownResource ConfigResourceType = iota
	//AnyResource constant type
	AnyResource
	//TopicResource constant type
	TopicResource
	//GroupResource constant type
	GroupResource
	//ClusterResource constant type
	ClusterResource
	//BrokerResource constant type
	BrokerResource
)
