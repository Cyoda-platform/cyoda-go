package observability

import (
	"context"
	"encoding/json"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

// TracingExternalProcessingService wraps an ExternalProcessingService with OTel spans and metrics.
type TracingExternalProcessingService struct {
	inner            spi.ExternalProcessingService
	tracer           trace.Tracer
	dispatchDuration metric.Float64Histogram
	dispatchTotal    metric.Int64Counter
}

// NewTracingExternalProcessingService returns a TracingExternalProcessingService that decorates
// inner with OTel tracing spans and metrics for processor and criteria dispatches.
func NewTracingExternalProcessingService(inner spi.ExternalProcessingService) *TracingExternalProcessingService {
	tracer := Tracer()
	meter := Meter()

	duration, _ := meter.Float64Histogram("cyoda.dispatch.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Processor/criteria dispatch duration"))
	total, _ := meter.Int64Counter("cyoda.dispatch.count",
		metric.WithDescription("Total dispatches"))

	return &TracingExternalProcessingService{
		inner:            inner,
		tracer:           tracer,
		dispatchDuration: duration,
		dispatchTotal:    total,
	}
}

func (t *TracingExternalProcessingService) DispatchProcessor(
	ctx context.Context, entity *common.Entity, processor common.ProcessorDefinition,
	workflowName, transitionName, txID string,
) (*common.Entity, error) {
	ctx, span := t.tracer.Start(ctx, "dispatch.processor", trace.WithAttributes(
		AttrProcessorName.String(processor.Name),
		AttrProcessorMode.String(processor.ExecutionMode),
		AttrProcessorTags.String(processor.Config.CalculationNodesTags),
		AttrWorkflowName.String(workflowName),
		AttrTransitionName.String(transitionName),
	))
	defer span.End()

	start := time.Now()
	result, err := t.inner.DispatchProcessor(ctx, entity, processor, workflowName, transitionName, txID)
	elapsed := time.Since(start).Seconds()

	t.dispatchDuration.Record(ctx, elapsed, metric.WithAttributes(
		AttrDispatchType.String("processor"),
	))
	t.dispatchTotal.Add(ctx, 1, metric.WithAttributes(AttrDispatchType.String("processor")))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	return result, err
}

func (t *TracingExternalProcessingService) DispatchCriteria(
	ctx context.Context, entity *common.Entity, criterion json.RawMessage,
	target, workflowName, transitionName, processorName, txID string,
) (bool, error) {
	ctx, span := t.tracer.Start(ctx, "dispatch.criteria", trace.WithAttributes(
		AttrCriterionTarget.String(target),
		AttrWorkflowName.String(workflowName),
		AttrTransitionName.String(transitionName),
	))
	defer span.End()

	start := time.Now()
	matches, err := t.inner.DispatchCriteria(ctx, entity, criterion, target, workflowName, transitionName, processorName, txID)
	elapsed := time.Since(start).Seconds()

	t.dispatchDuration.Record(ctx, elapsed, metric.WithAttributes(AttrDispatchType.String("criteria")))
	t.dispatchTotal.Add(ctx, 1, metric.WithAttributes(AttrDispatchType.String("criteria")))

	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.SetAttributes(AttrCriteriaMatches.Bool(matches))
	return matches, err
}
