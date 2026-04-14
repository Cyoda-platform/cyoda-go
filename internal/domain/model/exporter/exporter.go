package exporter

import "github.com/cyoda-platform/cyoda-go/internal/domain/model/schema"

// Exporter converts a ModelNode tree into an export format.
type Exporter interface {
	Export(node *schema.ModelNode) ([]byte, error)
}
