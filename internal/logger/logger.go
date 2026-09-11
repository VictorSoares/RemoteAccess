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

func InitLogger() *MemoryLogWriter {
	exePath, err := os.Executable()
	var logFilePath string
	if err == nil {
		logFilePath = filepath.Join(filepath.Dir(exePath), "remoteaccess.log")
	} else {
		logFilePath = "remoteaccess.log"
	}

	f, _ := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)

	mw := &MemoryLogWriter{
		lines:   make([]string, 0, 200),
		maxSize: 200,
		file:    f,
	}

	globalLogger = mw
	log.SetOutput(mw)
	log.SetFlags(log.Ltime | log.Lshortfile)
	return mw
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
