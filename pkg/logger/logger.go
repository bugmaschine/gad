package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
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
	logFile *os.File
)

// CustomHandler is a custom slog handler for pretty printing.
type CustomHandler struct {
	writer *terminalWriter
	opts   slog.HandlerOptions
	mu     *sync.Mutex
	attrs  []slog.Attr
	groups []string
}

type terminalWriter struct {
	mu sync.RWMutex
	w  io.Writer
}

func NewCustomHandler(w io.Writer, opts slog.HandlerOptions) *CustomHandler {
	return &CustomHandler{writer: &terminalWriter{w: w}, opts: opts, mu: &sync.Mutex{}}
}

func (w *terminalWriter) Set(next io.Writer) {
	w.mu.Lock()
	w.w = next
	w.mu.Unlock()
}

func (w *terminalWriter) Write(p []byte) (int, error) {
	w.mu.RLock()
	out := w.w
	w.mu.RUnlock()
	return out.Write(p)
}

func (w *terminalWriter) Flush() {
	w.mu.RLock()
	out := w.w
	w.mu.RUnlock()
	if flusher, ok := out.(interface{ Flush() }); ok {
		flusher.Flush()
	}
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
	h.writeAttrs(&buf, h.attrs)
	h.writeRecordAttrs(&buf, r)
	buf.WriteByte('\n')

	h.writer.Write(buf.Bytes())
	h.writer.Flush()

	if logFile != nil {
		var fileBuf bytes.Buffer
		fmt.Fprintf(&fileBuf, "%s %s > %s", timeStr, levelName, r.Message)
		h.writeAttrs(&fileBuf, h.attrs)
		h.writeRecordAttrs(&fileBuf, r)
		fileBuf.WriteByte('\n')
		logFile.Write(fileBuf.Bytes())
	}

	return nil
}

func (h *CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := h.clone()
	next.attrs = append(next.attrs, attrs...)
	return next
}

func (h *CustomHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}

	next := h.clone()
	next.groups = append(next.groups, name)
	return next
}

func (h *CustomHandler) clone() *CustomHandler {
	next := *h
	next.attrs = append([]slog.Attr(nil), h.attrs...)
	next.groups = append([]string(nil), h.groups...)
	return &next
}

func (h *CustomHandler) SetWriter(w io.Writer) {
	h.writer.Set(w)
}

func (h *CustomHandler) writeRecordAttrs(buf *bytes.Buffer, r slog.Record) {
	r.Attrs(func(a slog.Attr) bool {
		h.writeAttr(buf, a)
		return true
	})
}

func (h *CustomHandler) writeAttrs(buf *bytes.Buffer, attrs []slog.Attr) {
	for _, attr := range attrs {
		h.writeAttr(buf, attr)
	}
}

func (h *CustomHandler) writeAttr(buf *bytes.Buffer, attr slog.Attr) {
	key := attr.Key
	if len(h.groups) > 0 {
		key = strings.Join(append(append([]string(nil), h.groups...), key), ".")
	}
	fmt.Fprintf(buf, " %s=%v", key, attr.Value)
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
	if defaultHandler, ok := slog.Default().Handler().(*CustomHandler); ok {
		defaultHandler.SetWriter(w)
		return
	}

	slog.SetDefault(slog.New(NewCustomHandler(w, slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}

func ResetWriter() {
	SetWriter(os.Stderr)
}

func Close() {
	if logFile == nil {
		return
	}
	_ = logFile.Sync()
	_ = logFile.Close()
	logFile = nil
}
