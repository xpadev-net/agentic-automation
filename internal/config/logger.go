package config

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"
)

// Logger is the global logger instance that backs all PlainLogger adapters.
// It defaults to stdout with UTC timestamps and can be overridden for tests.
var Logger *log.Logger

var (
	levelMu      sync.RWMutex
	currentLevel = InfoLevel
)

// LogLevel represents the minimum severity that will be emitted.
type LogLevel int

const (
	// DebugLevel logs verbose debugging information.
	DebugLevel LogLevel = iota
	// InfoLevel logs routine operational information.
	InfoLevel
	// WarnLevel logs unexpected situations that do not halt execution.
	WarnLevel
	// ErrorLevel logs errors that require attention.
	ErrorLevel
	// FatalLevel logs critical errors and terminates the process.
	FatalLevel
)

func (l LogLevel) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	case FatalLevel:
		return "FATAL"
	default:
		return "INFO"
	}
}

// Field represents a single key/value pair formatted as [key=value].
type Field struct {
	key   string
	value string
	valid bool
}

func newField(key, value string) Field {
	if key == "" {
		return Field{}
	}
	return Field{key: key, value: value, valid: true}
}

func (f Field) format() (string, bool) {
	if !f.valid {
		return "", false
	}
	return fmt.Sprintf("[%s=%s]", f.key, f.value), true
}

// AppLogger wraps a standard logger and stores contextual fields.
type AppLogger struct {
	base   *log.Logger
	fields []Field
}

func newAppLogger(base *log.Logger, fields []Field) *AppLogger {
	if base == nil {
		base = defaultLogger()
	}
	copied := make([]Field, len(fields))
	copy(copied, fields)
	return &AppLogger{
		base:   base,
		fields: copied,
	}
}

// InitLogger initializes the global logger with configuration from environment variables.
func InitLogger() error {
	setLevel(os.Getenv("LOG_LEVEL"))
	Logger = log.New(os.Stdout, "", log.LstdFlags|log.LUTC|log.Lmicroseconds)
	return nil
}

// SetLoggerForTesting overrides the base logger. Intended for tests only.
func SetLoggerForTesting(testLogger *AppLogger) {
	if testLogger == nil {
		Logger = nil
		return
	}
	testLogger.ensureBase()
	Logger = testLogger.base
}

// ResetLoggerForTesting resets internal logger state to defaults.
func ResetLoggerForTesting() {
	Logger = nil
	setLevel("")
}

// GetLogger returns an AppLogger that writes to the global logger.
func GetLogger() *AppLogger {
	return newAppLogger(defaultLogger(), nil)
}

// NewNopLogger returns a logger that discards all output.
func NewNopLogger() *AppLogger {
	return newAppLogger(log.New(io.Discard, "", 0), nil)
}

// FromStdLogger wraps a standard library logger into an AppLogger.
func FromStdLogger(base *log.Logger) *AppLogger {
	return newAppLogger(base, nil)
}

// ContextWithTraceIDs attaches trace identifiers to the context. Empty values are ignored.
func ContextWithTraceIDs(ctx context.Context, githubEventID, agentRunID, operationID string) context.Context {
	if githubEventID != "" {
		ctx = context.WithValue(ctx, githubEventIDKey, githubEventID)
	}
	if agentRunID != "" {
		ctx = context.WithValue(ctx, agentRunIDKey, agentRunID)
	}
	if operationID != "" {
		ctx = context.WithValue(ctx, operationIDKey, operationID)
	}
	return ctx
}

// LoggerWithTraceIDs returns a logger pre-populated with trace identifiers extracted from context.
func LoggerWithTraceIDs(ctx context.Context) *AppLogger {
	return GetLogger().With(traceFieldsFromContext(ctx)...)
}

// WithTraceIDs augments the provided logger with explicit trace identifiers.
func WithTraceIDs(logger *AppLogger, githubEventID, agentRunID, operationID string) *AppLogger {
	if logger == nil {
		logger = GetLogger()
	}
	return logger.With(traceField("github_event_id", githubEventID),
		traceField("agent_run_id", agentRunID),
		traceField("operation_id", operationID),
	)
}

// contextKey is a type for context keys to avoid collisions
type contextKey string

const (
	// githubEventIDKey is the context key for GitHub event ID (X-GitHub-Delivery)
	githubEventIDKey contextKey = "github_event_id"
	// agentRunIDKey is the context key for agent run ID
	agentRunIDKey contextKey = "agent_run_id"
	// operationIDKey is the context key for operation ID
	operationIDKey contextKey = "operation_id"
)

func traceFieldsFromContext(ctx context.Context) []Field {
	var fields []Field
	if githubEventID := ctx.Value(githubEventIDKey); githubEventID != nil {
		if id, ok := githubEventID.(string); ok && id != "" {
			fields = append(fields, traceField("github_event_id", id))
		}
	}
	if agentRunID := ctx.Value(agentRunIDKey); agentRunID != nil {
		if id, ok := agentRunID.(string); ok && id != "" {
			fields = append(fields, traceField("agent_run_id", id))
		}
	}
	if operationID := ctx.Value(operationIDKey); operationID != nil {
		if id, ok := operationID.(string); ok && id != "" {
			fields = append(fields, traceField("operation_id", id))
		}
	}
	return fields
}

func traceField(key, value string) Field {
	if value == "" {
		return Field{}
	}
	return newField(key, value)
}

func (l *AppLogger) ensureBase() {
	if l.base == nil {
		l.base = defaultLogger()
	}
}

// With returns a new logger that includes the provided fields.
func (l *AppLogger) With(fields ...Field) *AppLogger {
	if l == nil {
		return GetLogger().With(fields...)
	}
	combined := make([]Field, 0, len(l.fields)+len(fields))
	for _, f := range l.fields {
		if f.valid {
			combined = append(combined, f)
		}
	}
	for _, f := range fields {
		if f.valid {
			combined = append(combined, f)
		}
	}
	return newAppLogger(l.base, combined)
}

// Debug logs a debug-level message.
func (l *AppLogger) Debug(message string, fields ...Field) {
	l.log(DebugLevel, message, fields...)
}

// Info logs an info-level message.
func (l *AppLogger) Info(message string, fields ...Field) {
	l.log(InfoLevel, message, fields...)
}

// Warn logs a warning-level message.
func (l *AppLogger) Warn(message string, fields ...Field) {
	l.log(WarnLevel, message, fields...)
}

// Error logs an error-level message.
func (l *AppLogger) Error(message string, fields ...Field) {
	l.log(ErrorLevel, message, fields...)
}

// Fatal logs a fatal-level message and terminates the process.
func (l *AppLogger) Fatal(message string, fields ...Field) {
	l.log(FatalLevel, message, fields...)
	os.Exit(1)
}

func (l *AppLogger) log(level LogLevel, message string, fields ...Field) {
	levelMu.RLock()
	threshold := currentLevel
	levelMu.RUnlock()
	if level < threshold {
		return
	}
	if l == nil {
		l = GetLogger()
	}
	l.ensureBase()
	var builder strings.Builder
	builder.WriteString("[")
	builder.WriteString(level.String())
	builder.WriteString("] ")
	builder.WriteString(message)
	combined := append(make([]Field, 0, len(l.fields)+len(fields)), l.fields...)
	combined = append(combined, fields...)
	for _, f := range combined {
		if formatted, ok := f.format(); ok {
			builder.WriteByte(' ')
			builder.WriteString(formatted)
		}
	}
	l.base.Println(builder.String())
}

func defaultLogger() *log.Logger {
	if Logger == nil {
		Logger = log.New(os.Stdout, "", log.LstdFlags|log.LUTC|log.Lmicroseconds)
	}
	return Logger
}

func setLevel(value string) {
	levelMu.Lock()
	defer levelMu.Unlock()
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		currentLevel = DebugLevel
	case "warn", "warning":
		currentLevel = WarnLevel
	case "error":
		currentLevel = ErrorLevel
	case "fatal":
		currentLevel = FatalLevel
	default:
		currentLevel = InfoLevel
	}
}

// Field helpers -------------------------------------------------------------

// String formats a string field as [key=value].
func String(key, value string) Field {
	return newField(key, value)
}

// Strings formats a slice of strings.
func Strings(key string, values []string) Field {
	return newField(key, strings.Join(values, ","))
}

// Bool formats a boolean field.
func Bool(key string, value bool) Field {
	return newField(key, fmt.Sprintf("%t", value))
}

// Int formats an int field.
func Int(key string, value int) Field {
	return newField(key, fmt.Sprintf("%d", value))
}

// Int64 formats an int64 field.
func Int64(key string, value int64) Field {
	return newField(key, fmt.Sprintf("%d", value))
}

// Uint64 formats an uint64 field.
func Uint64(key string, value uint64) Field {
	return newField(key, fmt.Sprintf("%d", value))
}

// Ints formats a slice of ints.
func Ints(key string, values []int) Field {
	return newField(key, fmt.Sprintf("%v", values))
}

// Duration formats a time.Duration field.
func Duration(key string, value time.Duration) Field {
	return newField(key, value.String())
}

// Time formats a time.Time field in RFC3339 format.
func Time(key string, value time.Time) Field {
	return newField(key, value.UTC().Format(time.RFC3339Nano))
}

// Any formats any value using fmt.Sprint.
// Pointers are automatically dereferenced to show their underlying values.
// Nil pointers are formatted as "nil".
func Any(key string, value any) Field {
	return newField(key, formatAnyValue(value))
}

// formatAnyValue formats a value, dereferencing pointers recursively.
func formatAnyValue(value any) string {
	if value == nil {
		return "nil"
	}

	v := reflect.ValueOf(value)
	// Handle pointers by dereferencing them
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return "nil"
		}
		v = v.Elem()
	}

	// Format the dereferenced value
	return fmt.Sprint(v.Interface())
}

// Error formats an error field. Nil errors are ignored.
func Error(err error) Field {
	if err == nil {
		return Field{}
	}
	return newField("error", err.Error())
}
