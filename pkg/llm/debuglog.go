package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const debugLogPath = "/tmp/iroha-debug.log"

var (
	debugMu   sync.Mutex
	debugFile *os.File
	debugOn   bool
)

// InitDebugLog opens the debug log file, truncating any previous content.
func InitDebugLog() {
	debugMu.Lock()
	defer debugMu.Unlock()

	if debugFile != nil {
		return
	}

	_ = os.MkdirAll(filepath.Dir(debugLogPath), 0755)
	f, err := os.Create(debugLogPath)
	if err != nil {
		return
	}
	debugFile = f
	debugOn = true
}

// DebugLog appends a timestamped line to the debug log.
func DebugLog(format string, args ...any) {
	if !debugOn {
		return
	}
	debugMu.Lock()
	defer debugMu.Unlock()

	if debugFile == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	msg := fmt.Sprintf("[%s] %s\n", ts, fmt.Sprintf(format, args...))
	debugFile.WriteString(msg)
}

// DumpDebugFile writes raw bytes to a companion file.
func DumpDebugFile(name string, data []byte) {
	if !debugOn {
		return
	}
	path := filepath.Join(filepath.Dir(debugLogPath), "iroha-debug-"+name)
	os.WriteFile(path, data, 0644)
}
