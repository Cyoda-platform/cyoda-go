package common

const (
	ErrCodeModelNotFound      = "MODEL_NOT_FOUND"
	ErrCodeModelNotLocked     = "MODEL_NOT_LOCKED"
	ErrCodeEntityNotFound     = "ENTITY_NOT_FOUND"
	ErrCodeValidationFailed   = "VALIDATION_FAILED"
	ErrCodeTransitionNotFound = "TRANSITION_NOT_FOUND"
	ErrCodeWorkflowNotFound   = "WORKFLOW_NOT_FOUND"
	ErrCodeWorkflowFailed     = "WORKFLOW_FAILED"
	ErrCodeConflict           = "CONFLICT"
	ErrCodeEpochMismatch      = "EPOCH_MISMATCH"
	ErrCodeBadRequest         = "BAD_REQUEST"
	ErrCodeUnauthorized       = "UNAUTHORIZED"
	ErrCodeForbidden          = "FORBIDDEN"
	ErrCodeServerError        = "SERVER_ERROR"
	ErrCodeNotImplemented     = "NOT_IMPLEMENTED"
)

const (
	ErrCodeTransactionNodeUnavailable = "TRANSACTION_NODE_UNAVAILABLE"
	ErrCodeTransactionExpired         = "TRANSACTION_EXPIRED"
	ErrCodeIdempotencyConflict        = "IDEMPOTENCY_CONFLICT"
	ErrCodeClusterNodeNotRegistered   = "CLUSTER_NODE_NOT_REGISTERED"
	ErrCodeTransactionNotFound        = "TRANSACTION_NOT_FOUND"
)

const (
	ErrCodeNoComputeMemberForTag     = "NO_COMPUTE_MEMBER_FOR_TAG"
	ErrCodeDispatchForwardFailed     = "DISPATCH_FORWARD_FAILED"
	ErrCodeDispatchTimeout           = "DISPATCH_TIMEOUT"
	ErrCodeComputeMemberDisconnected = "COMPUTE_MEMBER_DISCONNECTED"
)

const (
	ErrCodeTxRequired                 = "TX_REQUIRED"
	ErrCodeTxConflict                 = "TX_CONFLICT"
	ErrCodeTxCoordinatorNotConfigured = "TX_COORDINATOR_NOT_CONFIGURED"
	ErrCodeTxNoState                  = "TX_NO_STATE"
)

const (
	ErrCodeSearchJobNotFound        = "SEARCH_JOB_NOT_FOUND"
	ErrCodeSearchJobAlreadyTerminal = "SEARCH_JOB_ALREADY_TERMINAL"
	ErrCodeSearchShardTimeout       = "SEARCH_SHARD_TIMEOUT"
	ErrCodeSearchResultLimit        = "SEARCH_RESULT_LIMIT"
)
