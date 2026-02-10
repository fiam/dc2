package dc2_test

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/lmittmann/tint"
)

func TestMain(m *testing.M) {
	testLogDebug, _ := strconv.ParseBool(os.Getenv("DC2_TEST_LOG_DEBUG"))
	if testLogDebug {
		slog.SetDefault(slog.New(
			tint.NewHandler(os.Stderr, &tint.Options{
				Level:      slog.LevelDebug,
				TimeFormat: time.Kitchen,
			}),
		))
	}
	exitCode := m.Run()
	os.Exit(exitCode)
}
