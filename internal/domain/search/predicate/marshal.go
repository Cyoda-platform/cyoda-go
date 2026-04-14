package predicate

import "encoding/json"

// MarshalJSON for SimpleCondition emits the discriminator + struct fields.
func (c *SimpleCondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		JsonPath     string `json:"jsonPath"`
		OperatorType string `json:"operatorType"`
		Value        any    `json:"value"`
	}{
		Type:         c.Type(),
		JsonPath:     c.JsonPath,
		OperatorType: c.OperatorType,
		Value:        c.Value,
	})
}

// MarshalJSON for LifecycleCondition emits the discriminator + struct fields.
func (c *LifecycleCondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type         string `json:"type"`
		Field        string `json:"field"`
		OperatorType string `json:"operatorType"`
		Value        any    `json:"value"`
	}{
		Type:         c.Type(),
		Field:        c.Field,
		OperatorType: c.OperatorType,
		Value:        c.Value,
	})
}

// MarshalJSON for GroupCondition emits the discriminator + struct fields.
// Conditions is encoded as an empty array (not null) when there are no
// children, so the round-trip is symmetrical with the parser's expectation.
func (c *GroupCondition) MarshalJSON() ([]byte, error) {
	conditions := c.Conditions
	if conditions == nil {
		conditions = []Condition{}
	}
	return json.Marshal(struct {
		Type       string      `json:"type"`
		Operator   string      `json:"operator"`
		Conditions []Condition `json:"conditions"`
	}{
		Type:       c.Type(),
		Operator:   c.Operator,
		Conditions: conditions,
	})
}

// MarshalJSON for ArrayCondition emits the discriminator + struct fields.
func (c *ArrayCondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type     string `json:"type"`
		JsonPath string `json:"jsonPath"`
		Values   []any  `json:"values"`
	}{
		Type:     c.Type(),
		JsonPath: c.JsonPath,
		Values:   c.Values,
	})
}

// MarshalJSON for FunctionCondition emits only the discriminator.
func (c *FunctionCondition) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
	}{
		Type: c.Type(),
	})
}
