package importer

import "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"

// validateFieldName delegates to [schema.ValidateFieldName], the single
// definition of the rule. See that doc comment for the rationale.
//
// There is no importer-local ErrInvalidFieldName alias: every errors.Is
// classification site (here, ingest, and the model-import handler) matches
// on schema.ErrInvalidFieldName directly, so the sentinel has exactly one
// spelling.
func validateFieldName(parent, name string) error {
	return schema.ValidateFieldName(parent, name)
}
