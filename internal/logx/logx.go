// Package logx writes timestamped operational logs.
package logx

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Logger is a small thread-safe logger.
type Logger struct {
	mu     sync.Mutex
	writer io.Writer
	now    func() time.Time
}

// New returns a logger writing to w.
func New(w io.Writer) *Logger {
	return &Logger{writer: w, now: time.Now}
}

// Discard returns a logger that writes nothing.
func Discard() *Logger { return New(io.Discard) }

// Infof logs a normal event.
func (l *Logger) Infof(format string, args ...any) { l.write("info", format, args...) }

// Errorf logs a failure.
func (l *Logger) Errorf(format string, args ...any) { l.write("error", format, args...) }

func (l *Logger) write(level, format string, args ...any) {
	if l == nil || l.writer == nil {
		return
	}
	message := fmt.Sprintf(format, args...)
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.writer, "%s %-5s %s\n", l.now().UTC().Format(time.RFC3339), level, message)
}

// MultiWriter returns a writer that copies to every destination.
func MultiWriter(writers ...io.Writer) io.Writer {
	return io.MultiWriter(writers...)
}
