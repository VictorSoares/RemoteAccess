package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type MemoryLogWriter struct {
	mu      sync.RWMutex
	lines   []string
	maxSize int
	file    *os.File
}

var globalLogger *MemoryLogWriter

func InitLogger(enableFileLogging bool) *MemoryLogWriter {
	mw := &MemoryLogWriter{
		lines:   make([]string, 0, 300),
		maxSize: 300,
	}

	if enableFileLogging {
		mw.enableFile()
	}

	globalLogger = mw
	log.SetOutput(mw)
	log.SetFlags(log.Ltime | log.Lshortfile)
	return mw
}

func (m *MemoryLogWriter) enableFile() {
	appData := os.Getenv("APPDATA")
	var logFilePath string
	if appData != "" {
		dir := filepath.Join(appData, "RemoteAccess")
		_ = os.MkdirAll(dir, 0755)
		logFilePath = filepath.Join(dir, "remoteaccess.log")
	} else {
		logFilePath = "remoteaccess.log"
	}

	f, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		m.file = f
	}
}

func SetFileLogging(enable bool) {
	if globalLogger == nil {
		return
	}
	globalLogger.mu.Lock()
	defer globalLogger.mu.Unlock()

	if enable && globalLogger.file == nil {
		globalLogger.enableFile()
	} else if !enable && globalLogger.file != nil {
		_ = globalLogger.file.Close()
		globalLogger.file = nil
	}
}

func (m *MemoryLogWriter) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	text := string(p)
	if m.file != nil {
		_, _ = m.file.Write(p)
	}

	// Print to stdout if available
	_, _ = os.Stdout.Write(p)

	formatted := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), text)
	m.lines = append(m.lines, formatted)
	if len(m.lines) > m.maxSize {
		m.lines = m.lines[len(m.lines)-m.maxSize:]
	}

	return len(p), nil
}

func GetLogs() []string {
	if globalLogger == nil {
		return []string{}
	}
	globalLogger.mu.RLock()
	defer globalLogger.mu.RUnlock()

	result := make([]string, len(globalLogger.lines))
	copy(result, globalLogger.lines)
	return result
}
