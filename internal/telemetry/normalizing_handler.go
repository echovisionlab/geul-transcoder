package telemetry

import sharedtelemetry "github.com/echovisionlab/geul-telemetry"

// NewNormalizingHandler delegates to the shared telemetry normalizer.
var NewNormalizingHandler = sharedtelemetry.NewNormalizingHandler
