package predicate

// Condition is the common interface for all search predicate conditions.
type Condition interface {
	Type() string
}

// SimpleCondition matches a single JSON path against a value using an operator.
type SimpleCondition struct {
	JsonPath     string
	OperatorType string
	Value        any
}

func (c *SimpleCondition) Type() string { return "simple" }

// LifecycleCondition matches entity lifecycle metadata fields.
type LifecycleCondition struct {
	Field        string // "state", "creationDate", "previousTransition"
	OperatorType string
	Value        any
}

func (c *LifecycleCondition) Type() string { return "lifecycle" }

// GroupCondition combines multiple conditions with a logical operator.
type GroupCondition struct {
	Operator   string // "AND", "OR"
	Conditions []Condition
}

func (c *GroupCondition) Type() string { return "group" }

// ArrayCondition matches positional values in a JSON array.
// Null entries in Values mean "skip this position".
type ArrayCondition struct {
	JsonPath string
	Values   []any // positional values, nil = skip
}

func (c *ArrayCondition) Type() string { return "array" }

// FunctionCondition is a placeholder for server-side function predicates.
type FunctionCondition struct{}

func (c *FunctionCondition) Type() string { return "function" }
