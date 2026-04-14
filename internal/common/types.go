package common

import (
	"encoding/json"
	"fmt"
	"time"
)

type ModelRef struct {
	EntityName   string
	ModelVersion string
}

func (r ModelRef) String() string {
	return r.EntityName + "." + r.ModelVersion
}

type EntityMeta struct {
	ID                      string
	TenantID                TenantID
	ModelRef                ModelRef
	State                   string
	Version                 int64
	CreationDate            time.Time
	LastModifiedDate        time.Time
	TransactionID           string
	ChangeType              string // "CREATED", "UPDATED", "DELETED"
	ChangeUser              string // user ID who performed the change
	TransitionForLatestSave string
}

type Entity struct {
	Meta EntityMeta
	Data []byte
}

// EntityVersion represents a single version entry in an entity's change history.
type EntityVersion struct {
	Entity     *Entity
	ChangeType string
	User       string
	Timestamp  time.Time
	Version    int64
	Deleted    bool
}

// ModelState represents the lifecycle state of an entity model.
type ModelState string

const (
	ModelLocked   ModelState = "LOCKED"
	ModelUnlocked ModelState = "UNLOCKED"
)

// ChangeLevel controls which structural changes are permitted during data ingestion.
type ChangeLevel string

const (
	ChangeLevelArrayLength   ChangeLevel = "ARRAY_LENGTH"
	ChangeLevelArrayElements ChangeLevel = "ARRAY_ELEMENTS"
	ChangeLevelType          ChangeLevel = "TYPE"
	ChangeLevelStructural    ChangeLevel = "STRUCTURAL"
)

// validChangeLevels is the set of recognized ChangeLevel values.
var validChangeLevels = map[ChangeLevel]bool{
	ChangeLevelArrayLength:   true,
	ChangeLevelArrayElements: true,
	ChangeLevelType:          true,
	ChangeLevelStructural:    true,
}

// ValidateChangeLevel returns an error if the given string is not a known ChangeLevel.
func ValidateChangeLevel(s string) (ChangeLevel, error) {
	cl := ChangeLevel(s)
	if !validChangeLevels[cl] {
		return "", fmt.Errorf("%s: invalid change level %q; valid values: ARRAY_LENGTH, ARRAY_ELEMENTS, TYPE, STRUCTURAL", ErrCodeBadRequest, s)
	}
	return cl, nil
}

// ModelDescriptor holds the full metadata and schema for an entity model.
type ModelDescriptor struct {
	Ref         ModelRef
	State       ModelState
	ChangeLevel ChangeLevel
	UpdateDate  time.Time
	Schema      []byte
}

// MessageHeader holds the fixed AMQP-aligned headers for an edge message.
type MessageHeader struct {
	Subject         string
	ContentType     string
	ContentLength   int64
	ContentEncoding string
	MessageID       string // custom message ID from X-Message-ID header
	UserID          string
	Recipient       string
	ReplyTo         string
	CorrelationID   string
}

// MessageMetaData holds arbitrary key-value metadata for an edge message.
// Values preserve their original JSON types (string, number, bool, etc.).
type MessageMetaData struct {
	Values        map[string]any
	IndexedValues map[string]any
}

// --- Workflow types ---

// WorkflowDefinition represents a complete workflow configuration.
type WorkflowDefinition struct {
	Version      string                     `json:"version"`
	Name         string                     `json:"name"`
	Description  string                     `json:"desc,omitempty"`
	InitialState string                     `json:"initialState"`
	Active       bool                       `json:"active"`
	Criterion    json.RawMessage            `json:"criterion,omitempty"`
	States       map[string]StateDefinition `json:"states"`
}

// StateDefinition represents a state with its transitions.
type StateDefinition struct {
	Transitions []TransitionDefinition `json:"transitions,omitempty"`
}

// TransitionDefinition represents a single transition from a state.
type TransitionDefinition struct {
	Name       string                `json:"name"`
	Next       string                `json:"next"`
	Manual     bool                  `json:"manual"`
	Disabled   bool                  `json:"disabled,omitempty"`
	Criterion  json.RawMessage       `json:"criterion,omitempty"`
	Processors []ProcessorDefinition `json:"processors,omitempty"`
}

// ProcessorDefinition represents a processor attached to a transition.
type ProcessorDefinition struct {
	Type          string          `json:"type"`
	Name          string          `json:"name"`
	ExecutionMode string          `json:"executionMode,omitempty"`
	Config        ProcessorConfig `json:"config,omitempty"`
}

// ProcessorConfig holds configuration for a processor.
type ProcessorConfig struct {
	AttachEntity         bool   `json:"attachEntity,omitempty"`
	CalculationNodesTags string `json:"calculationNodesTags,omitempty"`
	ResponseTimeoutMs    int64  `json:"responseTimeoutMs,omitempty"`
	RetryPolicy          string `json:"retryPolicy,omitempty"`
	Context              string `json:"context,omitempty"`
}

// --- State machine event types ---

// StateMachineEventType represents the type of state machine event.
type StateMachineEventType string

const (
	SMEventStarted                    StateMachineEventType = "STATE_MACHINE_START"
	SMEventFinished                   StateMachineEventType = "STATE_MACHINE_FINISH"
	SMEventCancelled                  StateMachineEventType = "CANCEL"
	SMEventForcedSuccess              StateMachineEventType = "FORCE_SUCCESS"
	SMEventWorkflowFound              StateMachineEventType = "WORKFLOW_FOUND"
	SMEventWorkflowNotFound           StateMachineEventType = "WORKFLOW_NOT_FOUND"
	SMEventWorkflowSkipped            StateMachineEventType = "WORKFLOW_SKIP"
	SMEventTransitionMade             StateMachineEventType = "TRANSITION_MAKE"
	SMEventTransitionNotFound         StateMachineEventType = "TRANSITION_NOT_FOUND"
	SMEventTransitionCriterionNoMatch StateMachineEventType = "TRANSITION_NOT_MATCH_CRITERION"
	SMEventProcessCriterionNoMatch    StateMachineEventType = "PROCESS_NOT_MATCH_CRITERION"
	SMEventProcessingPaused           StateMachineEventType = "PAUSE_FOR_PROCESSING"
	SMEventStateProcessResult         StateMachineEventType = "STATE_PROCESS_RESULT"
)

// StateMachineEvent represents a single event in a state machine execution.
type StateMachineEvent struct {
	EventType     StateMachineEventType `json:"eventType"`
	EntityID      string                `json:"entityId"`
	TimeUUID      string                `json:"timeUuid"`
	State         string                `json:"state,omitempty"`
	TransactionID string                `json:"transactionId,omitempty"`
	Details       string                `json:"details"`
	Data          map[string]any        `json:"data,omitempty"`
	Timestamp     time.Time             `json:"timestamp"`
}

// ExecutionResult holds the outcome of a workflow engine execution.
type ExecutionResult struct {
	State      string
	Success    bool
	StopReason string
	Error      error
}
