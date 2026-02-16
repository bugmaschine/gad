package logger

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

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

// CustomHandler is a custom slog handler for pretty printing.
type CustomHandler struct {
	w    io.Writer
	opts slog.HandlerOptions
}

func NewCustomHandler(w io.Writer, opts slog.HandlerOptions) *CustomHandler {
	return &CustomHandler{w: w, opts: opts}
}

func (h *CustomHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

func (h *CustomHandler) Handle(_ context.Context, r slog.Record) error {
	levelName := levelNames[r.Level]
	if levelName == "" {
		levelName = r.Level.String()
	}

	colorAttr := levelColors[r.Level]
	var levelStr string
	if colorAttr != nil {
		levelStr = colorAttr.Sprint(levelName)
	} else {
		levelStr = levelName
	}

	timeStr := r.Time.Format("15:04:05.000")

	fmt.Fprintf(h.w, "%s %s > %s", timeStr, levelStr, r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(h.w, " %s=%v", a.Key, a.Value)
		return true
	})
	fmt.Fprintln(h.w)
	return nil
}

func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h // Simplified for now
}

func (h *CustomHandler) WithGroup(name string) slog.Handler {
	return h // Simplified for now
}

// InitDefaultLogger initializes the global logger with the specified debug level.
func InitDefaultLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	handler := NewCustomHandler(os.Stderr, slog.HandlerOptions{
		Level: level,
	})

	logger := slog.New(handler)
	slog.SetDefault(logger)
}
