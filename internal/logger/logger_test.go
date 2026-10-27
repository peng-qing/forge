package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func preserveGlobalState(t *testing.T) {
	t.Helper()
	previousLogger := slog.Default()
	previousLevel := gLogLevel.Level()
	previousRollover := gLogRollover.Swap(nil)
	t.Cleanup(func() {
		if current := gLogRollover.Swap(previousRollover); current != nil {
			_ = current.Close()
		}
		gLogLevel.Set(previousLevel)
		slog.SetDefault(previousLogger)
	})
}

func decodeJSONLines(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []map[string]any
	for {
		var record map[string]any
		err := decoder.Decode(&record)
		if errors.Is(err, io.EOF) {
			return records
		}
		if err != nil {
			t.Fatalf("decode log record: %v\noutput: %s", err, data)
		}
		records = append(records, record)
	}
}

func readJSONLog(t *testing.T, filename string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	return decodeJSONLines(t, data)
}

func TestNewHandlerJSON(t *testing.T) {
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	handler := newHandler(&output, " JSON ", false, level)

	fixedTime := time.Date(2024, 6, 11, 10, 0, 0, 123456789, time.FixedZone("UTC+8", 8*60*60))
	record := slog.NewRecord(fixedTime, slog.LevelInfo, "gateway started", 0)
	record.AddAttrs(slog.String("addr", ":8080"), slog.Int("workers", 8))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	records := decodeJSONLines(t, output.Bytes())
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	got := records[0]
	if got["time"] != "2024/06/11 10:00:00.123456" {
		t.Fatalf("time = %v", got["time"])
	}
	if got["level"] != "INFO" || got["msg"] != "gateway started" {
		t.Fatalf("built-in fields = %v", got)
	}
	if got["addr"] != ":8080" || got["workers"] != float64(8) {
		t.Fatalf("attributes = %v", got)
	}
	if _, exists := got["source"]; exists {
		t.Fatalf("source unexpectedly present: %v", got["source"])
	}
}

func TestNewHandlerDefaultsToText(t *testing.T) {
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelDebug)
	handler := newHandler(&output, "unknown", false, level)
	record := slog.NewRecord(
		time.Date(2024, 6, 11, 10, 0, 0, 1000, time.Local),
		slog.LevelWarn,
		"queue nearly full",
		0,
	)
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		`time="2024/06/11 10:00:00.000001"`,
		"level=WARN",
		`msg="queue nearly full"`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("text output %q does not contain %q", got, want)
		}
	}
}

func TestNewHandlerSourcePointsToCallSite(t *testing.T) {
	var output bytes.Buffer
	level := new(slog.LevelVar)
	level.Set(slog.LevelInfo)
	log := slog.New(newHandler(&output, JsonFormatter, true, level))

	_, file, line, _ := runtime.Caller(0)
	log.Info("with source")

	got := decodeJSONLines(t, output.Bytes())[0]
	source, ok := got["source"].(map[string]any)
	if !ok {
		t.Fatalf("source = %#v", got["source"])
	}
	if source["file"] != file || source["line"] != float64(line+1) {
		t.Fatalf("source = %v, want %s:%d", source, file, line+1)
	}
}

func TestSetLogLevelControlsConfiguredHandler(t *testing.T) {
	preserveGlobalState(t)
	var output bytes.Buffer
	gLogLevel.Set(slog.LevelInfo)
	slog.SetDefault(slog.New(newHandler(&output, JsonFormatter, false, &gLogLevel)))

	slog.Debug("hidden")
	slog.Info("visible info")
	if err := SetLogLevel("debug"); err != nil {
		t.Fatal(err)
	}
	slog.Debug("visible debug")

	records := decodeJSONLines(t, output.Bytes())
	if len(records) != 2 || records[0]["msg"] != "visible info" || records[1]["msg"] != "visible debug" {
		t.Fatalf("records = %v", records)
	}
	if got := GetLogLevel(); got != "DEBUG" {
		t.Fatalf("GetLogLevel() = %q, want DEBUG", got)
	}
	if err := SetLogLevel("not-a-level"); err == nil {
		t.Fatal("SetLogLevel accepted an invalid level")
	}
	if got := GetLogLevel(); got != "DEBUG" {
		t.Fatalf("invalid update changed level to %q", got)
	}
}

func TestInitConfigurationSetsSlogDefaultAndWritesFile(t *testing.T) {
	preserveGlobalState(t)
	dir := t.TempDir()
	config := &LogConfig{
		LogLevel:  "INFO",
		Format:    JsonFormatter,
		LogDir:    dir,
		AddSource: true,
	}
	previousLogger := slog.Default()
	if err := InitConfiguration("gateway", map[string]any{
		"service":   "gateway",
		"server_id": 7,
	}, config); err != nil {
		t.Fatal(err)
	}
	if slog.Default() == previousLogger {
		t.Fatal("InitConfiguration did not replace slog.Default")
	}

	slog.Debug("hidden")
	slog.Info("gateway started", "addr", ":8080")
	if err := Flush(); err != nil {
		t.Fatal(err)
	}

	records := readJSONLog(t, filepath.Join(dir, "gateway.log"))
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0]["msg"] != "gateway started" || records[0]["addr"] != ":8080" {
		t.Fatalf("record = %v", records[0])
	}
	if records[0]["service"] != "gateway" || records[0]["server_id"] != float64(7) {
		t.Fatalf("configured attributes = %v", records[0])
	}
	if _, exists := records[0]["source"]; !exists {
		t.Fatal("configured source field is missing")
	}
}

func TestInitConfigurationValidation(t *testing.T) {
	preserveGlobalState(t)
	if err := InitConfiguration("gateway", nil, nil); err == nil {
		t.Fatal("nil configuration was accepted")
	}
	before := slog.Default()
	config := &LogConfig{LogLevel: "verbose", LogDir: t.TempDir()}
	if err := InitConfiguration("gateway", nil, config); err == nil {
		t.Fatal("invalid level was accepted")
	}
	if slog.Default() != before {
		t.Fatal("invalid configuration replaced slog.Default")
	}
	if gLogRollover.Load() != nil {
		t.Fatal("invalid configuration installed a rollover")
	}
}

func TestLogConfigJSONTags(t *testing.T) {
	var config LogConfig
	data := []byte(`{"log_level":"WARN","format":"json","log_dir":"logs","console":true,"add_source":true,"max_size":10,"max_backups":3,"max_age":7,"compress":true}`)
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.LogLevel != "WARN" || config.Format != "json" || config.LogDir != "logs" || !config.Console || !config.AddSource || config.MaxSize != 10 || config.MaxBackups != 3 || config.MaxAge != 7 || !config.Compress {
		t.Fatalf("decoded config = %+v", config)
	}
}

func TestRotateFlushAndClose(t *testing.T) {
	preserveGlobalState(t)
	dir := t.TempDir()
	config := &LogConfig{LogLevel: "INFO", Format: JsonFormatter, LogDir: dir}
	if err := InitConfiguration("gateway", nil, config); err != nil {
		t.Fatal(err)
	}

	slog.Info("before rotate")
	if err := Flush(); err != nil {
		t.Fatal(err)
	}
	if err := Rotate(); err != nil {
		t.Fatal(err)
	}
	slog.Info("after rotate")
	if err := Flush(); err != nil {
		t.Fatal(err)
	}

	filename := filepath.Join(dir, "gateway.log")
	active := readJSONLog(t, filename)
	if len(active) != 1 || active[0]["msg"] != "after rotate" {
		t.Fatalf("active records = %v", active)
	}
	backups, err := filepath.Glob(filepath.Join(dir, "gateway_*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("backup count = %d, want 1: %v", len(backups), backups)
	}
	backup := readJSONLog(t, backups[0])
	if len(backup) != 1 || backup[0]["msg"] != "before rotate" {
		t.Fatalf("backup records = %v", backup)
	}

	if err := Close(); err != nil {
		t.Fatal(err)
	}
	if err := Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if gLogRollover.Load() != nil {
		t.Fatal("Close did not clear the active rollover")
	}
}

func TestLifecycleWithoutFileIsNoOp(t *testing.T) {
	preserveGlobalState(t)
	if err := Flush(); err != nil {
		t.Fatalf("Flush() = %v", err)
	}
	if err := Rotate(); err != nil {
		t.Fatalf("Rotate() = %v", err)
	}
	if err := Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestInitConfigurationReplacesPreviousFile(t *testing.T) {
	preserveGlobalState(t)
	dir := t.TempDir()
	config := &LogConfig{LogLevel: "INFO", Format: JsonFormatter, LogDir: dir}
	if err := InitConfiguration("first", nil, config); err != nil {
		t.Fatal(err)
	}
	slog.Info("first logger")
	if err := Flush(); err != nil {
		t.Fatal(err)
	}
	if err := InitConfiguration("second", nil, config); err != nil {
		t.Fatal(err)
	}
	slog.Info("second logger")
	if err := Flush(); err != nil {
		t.Fatal(err)
	}

	first := readJSONLog(t, filepath.Join(dir, "first.log"))
	second := readJSONLog(t, filepath.Join(dir, "second.log"))
	if len(first) != 1 || first[0]["msg"] != "first logger" {
		t.Fatalf("first records = %v", first)
	}
	if len(second) != 1 || second[0]["msg"] != "second logger" {
		t.Fatalf("second records = %v", second)
	}
}
