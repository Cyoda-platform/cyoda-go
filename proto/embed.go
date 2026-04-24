// Package proto exposes the raw .proto source files as embedded
// strings for CLI consumption (cyoda help grpc proto).
package proto

import _ "embed"

//go:embed cyoda/cyoda-cloud-api.proto
var CyodaCloudAPIProto string

//go:embed cloudevents/cloudevents.proto
var CloudEventsProto string
