package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/fatih/color"
)

// LevelTrace is a custom log level for trace logs.
const LevelTrace = slog.LevelDebug - 4

// Level names and colors
var levelNames = map[slog.Level]string{
	LevelTrace:      "TRACE",
	slog.LevelDebug: "DEBUG",
	slog.LevelInfo:  "INFO ",
	slog.LevelWarn:  "WARN ",
	slog.LevelError: "ERROR",
}

var levelColors = map[slog.Level]*color.Color{
	LevelTrace:      color.New(color.FgMagenta),
	slog.LevelDebug: color.New(color.FgBlue),
	slog.LevelInfo:  color.New(color.FgGreen),
	slog.LevelWarn:  color.New(color.FgYellow),
	slog.LevelError: color.New(color.FgRed),
}

var (
	logFile io.Writer
)

// CustomHandler is a custom slog handler for pretty printing.
type CustomHandler struct {
	w    io.Writer
	opts slog.HandlerOptions
	mu   *sync.Mutex
}

func NewCustomHandler(w io.Writer, opts slog.HandlerOptions) *CustomHandler {
	return &CustomHandler{w: w, opts: opts, mu: &sync.Mutex{}}
}

func (h *CustomHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *CustomHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	levelName := levelNames[r.Level]
	if levelName == "" {
		levelName = r.Level.String()
	}

	timeStr := r.Time.Format("15:04:05.000")

	colorAttr := levelColors[r.Level]
	var levelStr string
	if colorAttr != nil {
		levelStr = colorAttr.Sprint(levelName)
	} else {
		levelStr = levelName
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %s > %s", timeStr, levelStr, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&buf, " %s=%v", a.Key, a.Value)
		return true
	})
	buf.WriteByte('\n')

	h.w.Write(buf.Bytes())

	if flusher, ok := h.w.(interface{ Flush() }); ok {
		flusher.Flush()
	} else if syncer, ok := h.w.(interface{ Sync() error }); ok {
		_ = syncer.Sync()
	}

	if logFile != nil {
		var fileBuf bytes.Buffer
		fmt.Fprintf(&fileBuf, "%s %s > %s", timeStr, levelName, r.Message)
		r.Attrs(func(a slog.Attr) bool {
			fmt.Fprintf(&fileBuf, " %s=%v", a.Key, a.Value)
			return true
		})
		fileBuf.WriteByte('\n')
		logFile.Write(fileBuf.Bytes())
		if f, ok := logFile.(*os.File); ok {
			_ = f.Sync()
		}
	}

	return nil
}

func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // Simplified for now
}

func (h *CustomHandler) WithGroup(name string) slog.Handler {
	return h // Simplified for now
}

// InitDefaultLogger initializes the global logger with the specified debug level.
func InitDefaultLogger(debug bool, logFilePath string) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	var writer io.Writer = os.Stderr

	// only write to file if user set logfile path
	if logFilePath != "" {
		// os.O_APPEND: Add to the end of the file
		// os.O_CREATE: Create it if it doesn't exist
		// os.O_WRONLY: Open for writing only
		f, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open log file: %v\n", err)
		} else {
			logFile = f
		}
	}

	handler := NewCustomHandler(writer, slog.HandlerOptions{
		Level: level,
	})

	slog.SetDefault(slog.New(handler))
}

func SetWriter(w io.Writer) {
	currentLevel := slog.LevelInfo
	if defaultHandler, ok := slog.Default().Handler().(*CustomHandler); ok {
		currentLevel = defaultHandler.opts.Level.Level()
	}

	slog.SetDefault(slog.New(NewCustomHandler(w, slog.HandlerOptions{
		Level: currentLevel,
	})))
}

func ResetWriter() {
	SetWriter(os.Stderr)
}
