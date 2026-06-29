package diag

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	enabled = envBool("CROSSMEM_DEBUG")
	writer  io.Writer
)

func init() {
	if path := strings.TrimSpace(os.Getenv("CROSSMEM_LOG")); path != "" {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err == nil {
			writer = file
			enabled = true
		}
	}
	if writer == nil {
		writer = os.Stderr
	}
}

func Enabled() bool {
	return enabled
}

func Debugf(format string, args ...any) {
	if !enabled {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	ts := time.Now().UTC().Format(time.RFC3339)
	fmt.Fprintf(writer, "crossmem debug ts=%s %s\n", ts, fmt.Sprintf(format, args...))
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
