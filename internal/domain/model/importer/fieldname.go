package importer

import "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"

// ErrInvalidFieldName marks a field name that the wire jsonPath grammar cannot
// spell. It is an alias for [schema.ErrInvalidFieldName] — the check now
// lives in schema so schema.Admit can enforce the same rule, and importer
// keeps this name so callers matching on it with errors.Is do not have to
// change.
var ErrInvalidFieldName = schema.ErrInvalidFieldName

// validateFieldName delegates to [schema.ValidateFieldName], the single
// definition of the rule. See that doc comment for the rationale.
func validateFieldName(parent, name string) error {
	return schema.ValidateFieldName(parent, name)
}
