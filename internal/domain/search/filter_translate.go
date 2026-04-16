package search

import (
	"fmt"
	"strings"

	spi "github.com/cyoda-platform/cyoda-go-spi"
	"github.com/cyoda-platform/cyoda-go-spi/predicate"
)

// ConditionToFilter translates a domain predicate.Condition into an spi.Filter.
// This is the anti-corruption layer between the domain's predicate syntax and
// the SPI's stable filter contract used by storage plugins for pushdown.
func ConditionToFilter(cond predicate.Condition) (spi.Filter, error) {
	if cond == nil {
		return spi.Filter{}, fmt.Errorf("condition is nil")
	}

	switch c := cond.(type) {
	case *predicate.SimpleCondition:
		return simpleToFilter(c), nil
	case *predicate.LifecycleCondition:
		return lifecycleToFilter(c), nil
	case *predicate.GroupCondition:
		return groupToFilter(c)
	case *predicate.ArrayCondition:
		return arrayToFilter(c), nil
	case *predicate.FunctionCondition:
		return spi.Filter{}, fmt.Errorf("function conditions are not translatable to filters")
	default:
		return spi.Filter{}, fmt.Errorf("unsupported condition type: %T", cond)
	}
}

// simpleToFilter translates a SimpleCondition to a Filter with SourceData.
func simpleToFilter(c *predicate.SimpleCondition) spi.Filter {
	return spi.Filter{
		Op:     mapOperator(c.OperatorType),
		Path:   stripDollarDot(c.JsonPath),
		Source: spi.SourceData,
		Value:  c.Value,
	}
}

// lifecycleToFilter translates a LifecycleCondition to a Filter with SourceMeta.
func lifecycleToFilter(c *predicate.LifecycleCondition) spi.Filter {
	return spi.Filter{
		Op:     mapOperator(c.OperatorType),
		Path:   c.Field,
		Source: spi.SourceMeta,
		Value:  c.Value,
	}
}

// groupToFilter translates a GroupCondition to a Filter with AND/OR children.
func groupToFilter(c *predicate.GroupCondition) (spi.Filter, error) {
	op := spi.FilterAnd
	if strings.EqualFold(c.Operator, "OR") {
		op = spi.FilterOr
	}
	children := make([]spi.Filter, 0, len(c.Conditions))
	for _, child := range c.Conditions {
		f, err := ConditionToFilter(child)
		if err != nil {
			return spi.Filter{}, err
		}
		children = append(children, f)
	}
	return spi.Filter{Op: op, Children: children}, nil
}

// arrayToFilter translates an ArrayCondition. Array conditions are not directly
// translatable to SQL pushdown, so they are mapped to an op that forces
// post-filtering (matches_regex).
func arrayToFilter(c *predicate.ArrayCondition) spi.Filter {
	return spi.Filter{
		Op:     spi.FilterMatchesRegex, // forces post-filter
		Path:   stripDollarDot(c.JsonPath),
		Source: spi.SourceData,
	}
}

// stripDollarDot removes the leading "$." from a JSONPath expression.
// Domain conditions use JSONPath notation ("$.name"), but SPI filters
// use bare dot-notation ("name").
func stripDollarDot(path string) string {
	if len(path) > 2 && path[:2] == "$." {
		return path[2:]
	}
	return path
}

// mapOperator translates a domain operator string to a spi.FilterOp.
// Unknown operators are mapped to FilterMatchesRegex to force post-filtering.
func mapOperator(op string) spi.FilterOp {
	switch op {
	case "EQUALS":
		return spi.FilterEq
	case "NOT_EQUAL":
		return spi.FilterNe
	case "GREATER_THAN":
		return spi.FilterGt
	case "LESS_THAN":
		return spi.FilterLt
	case "GREATER_OR_EQUAL":
		return spi.FilterGte
	case "LESS_OR_EQUAL":
		return spi.FilterLte
	case "CONTAINS":
		return spi.FilterContains
	case "STARTS_WITH":
		return spi.FilterStartsWith
	case "ENDS_WITH":
		return spi.FilterEndsWith
	case "LIKE":
		return spi.FilterLike
	case "IS_NULL":
		return spi.FilterIsNull
	case "NOT_NULL":
		return spi.FilterNotNull
	case "BETWEEN":
		return spi.FilterBetween
	case "MATCHES_PATTERN":
		return spi.FilterMatchesRegex
	case "IEQUALS":
		return spi.FilterIEq
	case "INOT_EQUAL":
		return spi.FilterINe
	case "ICONTAINS":
		return spi.FilterIContains
	case "INOT_CONTAINS":
		return spi.FilterINotContains
	case "ISTARTS_WITH":
		return spi.FilterIStartsWith
	case "INOT_STARTS_WITH":
		return spi.FilterINotStartsWith
	case "IENDS_WITH":
		return spi.FilterIEndsWith
	case "INOT_ENDS_WITH":
		return spi.FilterINotEndsWith
	default:
		return spi.FilterMatchesRegex // forces post-filter for unknown ops
	}
}
