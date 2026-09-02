package telemetry

import sharedtelemetry "github.com/echovisionlab/geul-telemetry"

// NewFanoutHandler delegates to the shared telemetry fanout handler.
var NewFanoutHandler = sharedtelemetry.NewFanoutHandler
