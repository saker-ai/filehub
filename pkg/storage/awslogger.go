package storage

import (
	"fmt"
	"log/slog"

	smithylogging "github.com/aws/smithy-go/logging"
)

// awsLogger returns an aws.Logger that delegates AWS SDK log records to the
// process slog default.
//
// The SDK otherwise defaults aws.Config.Logger to its own logger writing to
// os.Stderr with an "SDK " prefix. Embedded in a host process that owns the
// terminal (saker's TUI) those lines land mid-render, and standalone they never
// reach the configured log sink. slog.Default() is resolved per record so the
// adapter follows a logger installed after the store was built.
func awsLogger() smithylogging.Logger {
	return smithylogging.LoggerFunc(func(classification smithylogging.Classification, format string, v ...any) {
		logger := slog.Default()
		msg := fmt.Sprintf(format, v...)
		switch classification {
		case smithylogging.Debug:
			logger.Debug(msg)
		case smithylogging.Warn:
			logger.Warn(msg)
		default:
			logger.Info(msg)
		}
	})
}
