package servicebus

type (
	// TrueFilter represents a always true sql expression which will accept all messages
	TrueFilter struct{}

	// FalseFilter represents a always false sql expression which will deny all messages
	FalseFilter struct{}

	// SQLFilter represents a SQL language-based filter expression that is evaluated against a BrokeredMessage. A
	// SQLFilter supports a subset of the SQL-92 standard.
	//
	// see: https://docs.microsoft.com/en-us/azure/service-bus-messaging/service-bus-messaging-sql-filter
	SQLFilter struct {
		Expression string
	}

	// CorrelationFilter holds a set of conditions that are matched against one or more of an arriving message's user
	// and system properties. A common use is to match against the CorrelationId property, but the application can also
	// choose to match against ContentType, Label, MessageId, ReplyTo, ReplyToSessionId, SessionId, To, and any
	// user-defined properties. A match exists when an arriving message's value for a property is equal to the value
	// specified in the correlation filter. For string expressions, the comparison is case-sensitive. When specifying
	// multiple match properties, the filter combines them as a logical AND condition, meaning for the filter to match,
	// all conditions must match.
	CorrelationFilter struct {
		CorrelationID    *string                `xml:"CorrelationId,omitempty"`
		MessageID        *string                `xml:"MessageId,omitempty"`
		To               *string                `xml:"To,omitempty"`
		ReplyTo          *string                `xml:"ReplyTo,omitempty"`
		Label            *string                `xml:"Label,omitempty"`
		SessionID        *string                `xml:"SessionId,omitempty"`
		ReplyToSessionID *string                `xml:"ReplyToSessionId,omitempty"`
		ContentType      *string                `xml:"ContentType,omitempty"`
		Properties       map[string]interface{} `xml:"Properties,omitempty"`
	}
)

// ToFilterDescription will transform the TrueFilter into a FilterDescription
func (tf TrueFilter) ToFilterDescription() FilterDescription {
	return FilterDescription{
		Type:          "TrueFilter",
		SQLExpression: ptrString("1=1"),
	}
}

// ToFilterDescription will transform the FalseFilter into a FilterDescription
func (ff FalseFilter) ToFilterDescription() FilterDescription {
	return FilterDescription{
		Type:          "FalseFilter",
		SQLExpression: ptrString("1!=1"),
	}
}

// ToFilterDescription will transform the SqlFilter into a FilterDescription
func (sf SQLFilter) ToFilterDescription() FilterDescription {
	return FilterDescription{
		Type:          "SqlFilter",
		SQLExpression: &sf.Expression,
	}
}

// ToFilterDescription will transform the CorrelationFilter into a FilterDescription
func (cf CorrelationFilter) ToFilterDescription() FilterDescription {
	return FilterDescription{
		Type:              "CorrelationFilter",
		CorrelationFilter: cf,
	}
}
