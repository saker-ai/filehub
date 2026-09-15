package storage

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	smithylogging "github.com/aws/smithy-go/logging"
)

func TestAWSLoggerForwardsToSlogDefault(t *testing.T) {
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)

	awsLogger().Logf(smithylogging.Warn, "skipped %s validation", "checksum")
	awsLogger().Logf(smithylogging.Debug, "request %d", 7)

	out := buf.String()
	for _, want := range []string{
		"level=WARN msg=\"skipped checksum validation\"",
		"level=DEBUG msg=\"request 7\"",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log output missing %q, got:\n%s", want, out)
		}
	}
}
