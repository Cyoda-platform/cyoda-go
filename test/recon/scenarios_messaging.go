//go:build cyoda_recon

package recon

// MessageCreateExclusions are fields excluded from message create response comparisons.
// Entity IDs and transaction IDs are generated dynamically and will differ between Cyoda-Go and Cyoda Cloud.
var MessageCreateExclusions = []string{
	"/entityIds",
	"/transactionId",
}

// MessageGetExclusions are fields excluded from message get response comparisons.
// Timestamps and encoding details may differ between implementations.
var MessageGetExclusions = []string{
	"/header/timestamp",
	"/header/contentLength",
	"/metaData/creationDate",
}

// messagingScenarios returns the messaging reconciliation flows.
func messagingScenarios() []Scenario {
	return []Scenario{
		messagingLifecycleFlow(),
	}
}

// messagingLifecycleFlow exercises create, get, delete, and get-after-delete for edge messages.
func messagingLifecycleFlow() Scenario {
	return Scenario{
		Name: "Messaging Lifecycle",
		Setup: func() map[string]string {
			return map[string]string{"subject": uniqueName("MsgLifecycle")}
		},
		Steps: []Step{
			// 1. Create message
			{
				Name:         "Create message",
				Method:       "POST",
				PathTemplate: "/message/new/{subject}",
				Body:         `{"payload": {"name": "Alice", "age": 30}}`,
				ExpectStatus: 200,
				Exclusions:   MessageCreateExclusions,
				Capture: map[string]string{
					"messageId": "0.entityIds.0",
				},
			},
			// 2. Get message
			{
				Name:         "Get message",
				Method:       "GET",
				PathTemplate: "/message/{messageId}",
				ExpectStatus: 200,
				Exclusions:   MessageGetExclusions,
			},
			// 3. Delete message
			{
				Name:         "Delete message",
				Method:       "DELETE",
				PathTemplate: "/message/{messageId}",
				ExpectStatus: 200,
				Exclusions:   []string{"/entityIds"},
			},
			// 4. Get deleted message — expect 404
			{
				Name:         "Get deleted message — expect 404",
				Method:       "GET",
				PathTemplate: "/message/{messageId}",
				ExpectStatus: 404,
				Exclusions:   []string{"/instance", "/detail", "/properties/messageId"},
			},
		},
	}
}
