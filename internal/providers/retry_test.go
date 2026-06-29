package providers

import (
	"errors"
	"testing"
)

func TestWithRetryRetriesTransientErrors(t *testing.T) {
	attempts := 0
	value, err := withRetry("test transient", func() (string, error) {
		attempts++
		if attempts < 2 {
			return "", errors.New("database is locked")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("withRetry returned error: %v", err)
	}
	if value != "ok" {
		t.Fatalf("unexpected value: %q", value)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestWithRetryDoesNotRetryPermanentErrors(t *testing.T) {
	attempts := 0
	_, err := withRetry("test permanent", func() (string, error) {
		attempts++
		return "", errors.New("permission denied")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}
