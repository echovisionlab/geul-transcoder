package app

import (
	"context"
	"time"

	sharedtelemetry "github.com/echovisionlab/geul-telemetry"
	apptelemetry "github.com/echovisionlab/geul-transcoder/internal/telemetry"
)

const (
	serviceComponent           = "worker"
	telemetryPipelineComponent = "otel_sdk"
)

func emitServiceReady(ctx context.Context) {
	record, err := sharedtelemetry.NewServiceReadyRecord(
		apptelemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
	)
	_ = apptelemetry.EmitSystem(ctx, record, err)
}

func emitServiceStopping(ctx context.Context) {
	record, err := sharedtelemetry.NewServiceStoppingRecord(
		apptelemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
	)
	_ = apptelemetry.EmitSystem(ctx, record, err)
}

func emitServiceFailed(ctx context.Context, failure error) {
	record, err := sharedtelemetry.NewServiceFailedRecord(
		apptelemetry.SystemMetadata(ctx, time.Now()), serviceComponent,
		sharedtelemetry.SystemFailure{ErrorCode: sharedtelemetry.StableErrorType(failure)},
	)
	_ = apptelemetry.EmitSystem(ctx, record, err)
}

func emitTelemetryPipelineDegraded(ctx context.Context, failure error) {
	record, err := sharedtelemetry.NewTelemetryPipelineDegradedRecord(
		apptelemetry.SystemMetadata(ctx, time.Now()), telemetryPipelineComponent,
		sharedtelemetry.SystemFailure{ErrorCode: sharedtelemetry.StableErrorType(failure)},
	)
	_ = apptelemetry.EmitSystem(ctx, record, err)
}
