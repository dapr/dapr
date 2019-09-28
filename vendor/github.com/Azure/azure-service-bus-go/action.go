package servicebus

type (
	// SQLAction represents a SQL language-based action expression that is evaluated against a BrokeredMessage. A
	// SQLAction supports a subset of the SQL-92 standard.
	//
	// With SQL filter conditions, you can define an action that can annotate the message by adding, removing, or
	// replacing properties and their values. The action uses a SQL-like expression that loosely leans on the SQL
	// UPDATE statement syntax. The action is performed on the message after it has been matched and before the message
	// is selected into the subscription. The changes to the message properties are private to the message copied into
	// the subscription.
	//
	// see: https://docs.microsoft.com/en-us/azure/service-bus-messaging/service-bus-messaging-sql-filter
	SQLAction struct {
		Expression string
	}
)

// ToActionDescription will transform the SqlAction into a ActionDescription
func (sf SQLAction) ToActionDescription() ActionDescription {
	return ActionDescription{
		Type:          "SqlRuleAction",
		SQLExpression: sf.Expression,
	}
}
