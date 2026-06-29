package providers

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/muthuishere/crossmemcli/internal/diag"
)

func withRetry[T any](label string, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	delays := []time.Duration{0, 25 * time.Millisecond, 75 * time.Millisecond}
	for attempt, delay := range delays {
		if delay > 0 {
			time.Sleep(delay)
		}
		value, err := fn()
		if err == nil {
			if attempt > 0 {
				diag.Debugf("retry label=%q attempts=%d result=ok", label, attempt+1)
			}
			return value, nil
		}
		last = err
		if !isTransient(err) {
			return zero, err
		}
		diag.Debugf("retry label=%q attempt=%d err=%q", label, attempt+1, err)
	}
	return zero, last
}

func isTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return false
	}
	text := err.Error()
	return containsAny(text, []string{
		"database is locked",
		"database table is locked",
		"busy",
		"resource temporarily unavailable",
		"interrupted system call",
		"too many open files",
	})
}

func containsAny(text string, needles []string) bool {
	text = strings.ToLower(text)
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}
