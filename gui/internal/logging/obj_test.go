package logging

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/voluminor/dji-rc-joystick/target"
	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"
)

// // // // // // // // // //

func defaultLogging() targetsettings.LoggingObj {
	return targetsettings.New().Logging
}

func TestNewWritesIndependentConsoleAndFileLevels(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	configRoot := t.TempDir()
	configPath := filepath.Join(configRoot, "settings.json")
	config := defaultLogging()
	var console bytes.Buffer

	obj, err := newWithConsole(config, configPath, &console)
	if err != nil {
		t.Fatalf("newWithConsole() error = %v", err)
	}

	logger := obj.Logger()
	logger.Trace().Msg("trace record")
	logger.Debug().Msg("debug record")
	logger.Info().Msg("info record")
	logger.Warn().Msg("warn record")
	logger.Error().Msg("error record")
	if err = obj.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err = obj.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	consoleText := console.String()
	if strings.Contains(consoleText, "trace record") || strings.Contains(consoleText, "debug record") {
		t.Fatalf("console contains records below info: %q", consoleText)
	}
	for _, message := range []string{"info record", "warn record", "error record"} {
		if !strings.Contains(consoleText, message) {
			t.Fatalf("console does not contain %q: %q", message, consoleText)
		}
	}
	if !strings.Contains(consoleText, "\x1b[") {
		t.Fatalf("console output is not colored: %q", consoleText)
	}

	expectedPath := filepath.Join(configRoot, "logs", target.Name+".log")
	if obj.FilePath() != expectedPath {
		t.Fatalf("FilePath() = %q, want %q", obj.FilePath(), expectedPath)
	}
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	lines := strings.FieldsFunc(strings.TrimSpace(string(data)), func(value rune) bool {
		return value == '\n'
	})
	if len(lines) != 1 {
		t.Fatalf("JSON log lines = %d, want 1: %q", len(lines), data)
	}
	var record map[string]any
	if err = json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatalf("parse JSON log: %v", err)
	}
	if record["level"] != "error" || record["message"] != "error record" {
		t.Fatalf("JSON log record = %#v", record)
	}

	info, err := os.Stat(expectedPath)
	if err != nil {
		t.Fatalf("stat log file: %v", err)
	}
	if permissions := info.Mode().Perm(); runtime.GOOS != "windows" && permissions != 0o600 {
		t.Fatalf("log permissions = %o, want 600", permissions)
	}
	directoryInfo, err := os.Stat(filepath.Dir(expectedPath))
	if err != nil {
		t.Fatalf("stat log directory: %v", err)
	}
	if permissions := directoryInfo.Mode().Perm(); runtime.GOOS != "windows" && permissions != 0o700 {
		t.Fatalf("log directory permissions = %o, want 700", permissions)
	}
}

func TestNewSupportsConsoleOnly(t *testing.T) {
	config := defaultLogging()
	config.File.Enabled = false
	var console bytes.Buffer

	obj, err := newWithConsole(config, filepath.Join(t.TempDir(), "settings.json"), &console)
	if err != nil {
		t.Fatalf("newWithConsole() error = %v", err)
	}
	logger := obj.Logger()
	logger.Info().Msg("console only")
	if err = obj.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if obj.FilePath() != "" {
		t.Fatalf("FilePath() = %q, want empty", obj.FilePath())
	}
	if !strings.Contains(console.String(), "console only") {
		t.Fatalf("console output = %q", console.String())
	}
}

func TestNewSupportsFileOnly(t *testing.T) {
	configRoot := t.TempDir()
	config := defaultLogging()
	config.Console.Enabled = false

	obj, err := newWithConsole(
		config,
		filepath.Join(configRoot, "settings.json"),
		nil,
	)
	if err != nil {
		t.Fatalf("newWithConsole() error = %v", err)
	}
	logger := obj.Logger()
	logger.Error().Msg("file only")
	if err = obj.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	data, err := os.ReadFile(obj.FilePath())
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), `"message":"file only"`) {
		t.Fatalf("file output = %q", data)
	}
}

func TestNewRejectsDisabledOutputs(t *testing.T) {
	config := defaultLogging()
	config.Console.Enabled = false
	config.File.Enabled = false

	if _, err := newWithConsole(config, filepath.Join(t.TempDir(), "settings.json"), &bytes.Buffer{}); err == nil {
		t.Fatal("newWithConsole() accepted disabled outputs")
	}
}

func TestNewRejectsNonRegularLogPath(t *testing.T) {
	configRoot := t.TempDir()
	logDirectory := filepath.Join(configRoot, "logs")
	if err := os.Mkdir(logDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(logDirectory, target.Name+".log"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := newWithConsole(
		defaultLogging(),
		filepath.Join(configRoot, "settings.json"),
		&bytes.Buffer{},
	); err == nil {
		t.Fatal("newWithConsole() accepted a non-regular log path")
	}
}

func TestLoggerSerializesConcurrentSinkWrites(t *testing.T) {
	config := defaultLogging()
	config.File.Enabled = false
	var console bytes.Buffer

	obj, err := newWithConsole(
		config,
		filepath.Join(t.TempDir(), "settings.json"),
		&console,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = obj.Close()
	})

	logger := obj.Logger()
	const workers = 16
	const recordsPerWorker = 64
	var group sync.WaitGroup
	group.Add(workers)
	for worker := 0; worker < workers; worker++ {
		go func(id int) {
			defer group.Done()
			for record := 0; record < recordsPerWorker; record++ {
				logger.Info().
					Int("worker", id).
					Int("record", record).
					Msg("concurrent record")
			}
		}(worker)
	}
	group.Wait()

	if records := strings.Count(console.String(), "concurrent record"); records != workers*recordsPerWorker {
		t.Fatalf(
			"serialized records = %d, want %d",
			records,
			workers*recordsPerWorker,
		)
	}
}

func TestConsoleEscapesRecordBreakingMessageCharacters(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	config := defaultLogging()
	config.File.Enabled = false
	var console bytes.Buffer

	obj, err := newWithConsole(
		config,
		filepath.Join(t.TempDir(), "settings.json"),
		&console,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = obj.Close()
	})

	logger := obj.Logger()
	logger.Info().
		Str("name", "third\n\x1b[32mfourth").
		Str("error", "fifth\r\n\x1b[33msixth").
		Int("count", 7).
		Msg("first\n\x1b[31msecond")
	text := console.String()
	if strings.Count(text, "\n") != 1 {
		t.Fatalf("console log was split into multiple records: %q", text)
	}
	if strings.Contains(text, "\x1b") {
		t.Fatalf("console log contains an injected ANSI escape: %q", text)
	}
	if !strings.Contains(text, `first\n\x1b[31msecond`) {
		t.Fatalf("escaped console message is missing: %q", text)
	}
	for _, field := range []string{
		`name="third\n\x1b[32mfourth"`,
		`error="fifth\r\n\x1b[33msixth"`,
		`count=7`,
	} {
		if !strings.Contains(text, field) {
			t.Fatalf("safe console field %q is missing: %q", field, text)
		}
	}
}
