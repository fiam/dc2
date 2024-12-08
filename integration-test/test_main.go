package dc2

import (
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/lmittmann/tint"
)

func TestMain(m *testing.M) {
	exitCode := m.Run()
	testLogDebug, _ := strconv.ParseBool(os.Getenv("DC2_TEST_LOG_DEBUG"))
	if testLogDebug {
		slog.SetDefault(slog.New(
			tint.NewHandler(os.Stderr, &tint.Options{
				Level:      slog.LevelDebug,
				TimeFormat: time.Kitchen,
			}),
		))
	}
	os.Exit(exitCode)
}
