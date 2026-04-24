package search

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cyoda-platform/cyoda-go-spi/predicate"
)

// canonicalOperators is the single source of truth for the valid
// `operatorType` values accepted by Simple / Lifecycle / Array conditions.
// The list is mirrored in cmd/cyoda/help/content/search.md — any change to
// one must be reflected in the other. Issue #90 closed the gap where
// unknown operator strings silently fell through to a regex match.
var canonicalOperators = map[string]struct{}{
	"EQUALS":           {},
	"NOT_EQUAL":        {},
	"GREATER_THAN":     {},
	"LESS_THAN":        {},
	"GREATER_OR_EQUAL": {},
	"LESS_OR_EQUAL":    {},
	"CONTAINS":         {},
	"STARTS_WITH":      {},
	"ENDS_WITH":        {},
	"LIKE":             {},
	"IS_NULL":          {},
	"NOT_NULL":         {},
	"BETWEEN":          {},
	"MATCHES_PATTERN":  {},
	"IEQUALS":          {},
	"INOT_EQUAL":       {},
	"ICONTAINS":        {},
	"INOT_CONTAINS":    {},
	"ISTARTS_WITH":     {},
	"INOT_STARTS_WITH": {},
	"IENDS_WITH":       {},
	"INOT_ENDS_WITH":   {},
}

// ValidateCondition walks a parsed condition tree and returns an error
// identifying any unknown operator. The returned error text lists the
// canonical set so callers can self-correct.
func ValidateCondition(cond predicate.Condition) error {
	if cond == nil {
		return nil
	}
	switch c := cond.(type) {
	case *predicate.SimpleCondition:
		return validateOperator(c.OperatorType)
	case *predicate.LifecycleCondition:
		return validateOperator(c.OperatorType)
	case *predicate.ArrayCondition:
		// ArrayCondition doesn't carry an operator — each positional value
		// becomes an equality check in arrayToFilter. Nothing to validate.
		_ = c
		return nil
	case *predicate.GroupCondition:
		for _, child := range c.Conditions {
			if err := ValidateCondition(child); err != nil {
				return err
			}
		}
		return nil
	case *predicate.FunctionCondition:
		// Function conditions are not operator-typed; nothing to check.
		return nil
	default:
		return nil
	}
}

func validateOperator(op string) error {
	if op == "" {
		return fmt.Errorf("missing operatorType; valid: %s", canonicalOperatorList())
	}
	if _, ok := canonicalOperators[op]; !ok {
		return fmt.Errorf("unknown operatorType %q; valid: %s", op, canonicalOperatorList())
	}
	return nil
}

// canonicalOperatorList returns a deterministic comma-separated list of
// canonical operators for inclusion in error responses.
func canonicalOperatorList() string {
	ops := make([]string, 0, len(canonicalOperators))
	for k := range canonicalOperators {
		ops = append(ops, k)
	}
	sort.Strings(ops)
	return strings.Join(ops, ", ")
}
