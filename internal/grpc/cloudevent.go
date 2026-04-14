package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	cepb "github.com/cyoda-platform/cyoda-go/api/grpc/cloudevents"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// NewCloudEvent creates a CloudEvent with JSON-marshalled payload as text data.
func NewCloudEvent(eventType string, payload any) (*cepb.CloudEvent, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal CloudEvent payload: %w", err)
	}

	return &cepb.CloudEvent{
		Id:          uuid.New().String(),
		Source:      "cyoda-go",
		SpecVersion: "1.0",
		Type:        eventType,
		Data:        &cepb.CloudEvent_TextData{TextData: string(data)},
	}, nil
}

// AttachAuthContext adds CloudEvents Auth Context extension attributes to a CloudEvent
// based on the UserContext in the request context.
// See: https://github.com/cloudevents/spec/blob/main/cloudevents/extensions/authcontext.md
func AttachAuthContext(ctx context.Context, ce *cepb.CloudEvent) {
	uc := common.GetUserContext(ctx)
	if uc == nil || ce == nil {
		return
	}

	if ce.Attributes == nil {
		ce.Attributes = make(map[string]*cepb.CloudEvent_CloudEventAttributeValue)
	}

	// Determine auth type based on roles.
	authType := "user"
	for _, role := range uc.Roles {
		if role == "ROLE_M2M" {
			authType = "service_account"
			break
		}
	}

	ce.Attributes["authtype"] = &cepb.CloudEvent_CloudEventAttributeValue{
		Attr: &cepb.CloudEvent_CloudEventAttributeValue_CeString{CeString: authType},
	}
	ce.Attributes["authid"] = &cepb.CloudEvent_CloudEventAttributeValue{
		Attr: &cepb.CloudEvent_CloudEventAttributeValue_CeString{CeString: uc.UserID},
	}

	// Claims: roles as comma-separated string.
	if len(uc.Roles) > 0 {
		ce.Attributes["authclaims"] = &cepb.CloudEvent_CloudEventAttributeValue{
			Attr: &cepb.CloudEvent_CloudEventAttributeValue_CeString{CeString: strings.Join(uc.Roles, ",")},
		}
	}
}

// ParseCloudEvent extracts the event type and raw JSON payload from a CloudEvent.
// Supports both TextData (string) and BinaryData (bytes) variants.
func ParseCloudEvent(ce *cepb.CloudEvent) (eventType string, payload json.RawMessage, err error) {
	if ce == nil {
		return "", nil, errors.New("cloud event is nil")
	}

	switch d := ce.Data.(type) {
	case *cepb.CloudEvent_TextData:
		return ce.Type, json.RawMessage(d.TextData), nil
	case *cepb.CloudEvent_BinaryData:
		return ce.Type, json.RawMessage(d.BinaryData), nil
	default:
		return "", nil, fmt.Errorf("unsupported CloudEvent data variant: %T", ce.Data)
	}
}

// ExtractTransactionID extracts the "transactionId" string field from a JSON payload.
// Returns "" if the field is absent or not a string.
func ExtractTransactionID(payload json.RawMessage) string {
	return ExtractStringField(payload, "transactionId")
}

// ExtractStringField extracts a string field by name from a JSON payload.
// Returns "" if the field is absent, not a string, or the payload is invalid JSON.
func ExtractStringField(payload json.RawMessage, field string) string {
	var m map[string]any
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}
