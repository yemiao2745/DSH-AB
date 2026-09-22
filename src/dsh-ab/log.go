package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	LevelOff  = "off"
	LevelAuto = "auto"
	LevelFull = "full"
)

func validLogLevel(l string) bool {
	return l == LevelOff || l == LevelAuto || l == LevelFull
}

// Logger is the single place that writes dsh-ab.log. off keeps errors only, auto
// adds warnings and debug lines, full records everything - info lines and the
// child's output included.
type Logger struct {
	mu       sync.Mutex
	dir      string
	level    string
	maxFiles int
	maxSize  int64
	file     *os.File
	size     int64
	wrote    bool
	// writeErr is the most recent reason a write did not reach the disk. Every
	// level writes something (off keeps errors), so this is what tells "nothing
	// has happened yet" apart from "the directory cannot be written"; the
	// popups need the second one to be honest about a log file the user will not
	// find.
	writeErr error
}

func NewLogger(dir, level string, maxFiles, maxSizeMB int) *Logger {
	if !validLogLevel(level) {
		level = LevelAuto
	}
	if maxFiles < 1 {
		maxFiles = 1
	}
	if maxSizeMB < 1 {
		maxSizeMB = 1
	}
	return &Logger{
		dir:      dir,
		level:    level,
		maxFiles: maxFiles,
		maxSize:  int64(maxSizeMB) * 1024 * 1024,
	}
}

func (l *Logger) Level() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.level
}

func (l *Logger) SetLevel(level string) {
	if !validLogLevel(level) {
		return
	}
	l.mu.Lock()
	l.level = level
	l.mu.Unlock()
}

// NextLevel cycles off -> auto -> full -> off, exactly what menu item 3 does.
func (l *Logger) NextLevel() string {
	switch l.Level() {
	case LevelOff:
		return LevelAuto
	case LevelAuto:
		return LevelFull
	default:
		return LevelOff
	}
}

// Path is the live log file, meaningful once Wrote reports true.
func (l *Logger) Path() string { return filepath.Join(l.dir, "dsh-ab.log") }

// Wrote reports whether anything has been written to disk in this process.
func (l *Logger) Wrote() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.wrote
}

// WriteError is why the most recent write failed, or nil when every attempted
// write reached the disk (and while nothing has been attempted). It stays set
// once a write has failed: a later line that lands does not undo the fact the
// caller was told about.
func (l *Logger) WriteError() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeErr
}

// The first argument of write is the level a line starts at: off keeps errors,
// auto adds warnings and debug, full also keeps info lines and child output.
func (l *Logger) Info(format string, args ...any)  { l.write(LevelFull, "INFO ", format, args...) }
func (l *Logger) Warnf(format string, args ...any) { l.write(LevelAuto, "WARN ", format, args...) }
func (l *Logger) Errorf(format string, args ...any) {
	l.write(LevelOff, "ERROR", format, args...)
}
func (l *Logger) Debugf(format string, args ...any) { l.write(LevelAuto, "DEBUG", format, args...) }

// Child records one line of the dsh child's output, tagged with the slot that
// child was started from. The log accumulates across restarts, so after a slot
// switch one file holds the output of both slots and every line has to say which
// one produced it (<slot> is slot-a or slot-b, so the tags are child-slot-a and
// child-slot-b). Blank lines are dropped.
func (l *Logger) Child(slot, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	l.write(LevelFull, "child-"+slot, "%s", line)
}

// write stores one line if the active level is at least as verbose as minLevel.
func (l *Logger) write(minLevel, tag, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if levelRank(l.level) < levelRank(minLevel) {
		return
	}
	if err := l.ensureLocked(); err != nil {
		l.writeErr = err
		return
	}
	line := fmt.Sprintf("%s %s %s\n", time.Now().Format("2006-01-02 15:04:05"), tag, fmt.Sprintf(format, args...))
	if l.size+int64(len(line)) > l.maxSize {
		l.rotateLocked()
	}
	n, err := l.file.WriteString(line)
	l.size += int64(n)
	if err != nil {
		l.writeErr = err
		return
	}
	l.wrote = true
}

// levelRank orders the levels by how much they keep. off is rank 0 and is still a
// real gate: it is the minimum an error line needs, so errors are written at every
// level including off.
func levelRank(level string) int {
	switch level {
	case LevelAuto:
		return 1
	case LevelFull:
		return 2
	default:
		return 0
	}
}

func (l *Logger) ensureLocked() error {
	if l.file != nil {
		return nil
	}
	if l.dir == "" {
		return fmt.Errorf("no log directory")
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	l.file, l.size, l.wrote = f, st.Size(), true
	return nil
}

// rotateLocked keeps at most maxFiles files in total: dsh-ab.log plus
// dsh-ab.1.log .. dsh-ab.<maxFiles-1>.log. maxFiles < 2 means the live log only.
func (l *Logger) rotateLocked() {
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
	oldest := l.maxFiles - 1
	if oldest < 1 {
		os.Remove(l.Path())
	} else {
		os.Remove(l.rotated(oldest))
		for i := oldest - 1; i >= 1; i-- {
			os.Rename(l.rotated(i), l.rotated(i+1))
		}
		os.Rename(l.Path(), l.rotated(1))
	}
	l.size = 0
	if err := l.ensureLocked(); err != nil {
		l.file = nil
	}
}

func (l *Logger) rotated(i int) string {
	return filepath.Join(l.dir, fmt.Sprintf("dsh-ab.%d.log", i))
}

func (l *Logger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		l.file.Close()
		l.file = nil
	}
}
