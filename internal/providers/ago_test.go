package providers

import (
	"testing"
	"time"
)

func TestRelativeAgo(t *testing.T) {
	now := time.Now()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{10 * time.Second, "just now"},
		{5 * time.Minute, "5 mins ago"},
		{15 * time.Hour, "15 hours ago"},
		{2 * 24 * time.Hour, "2 days ago"},
	}
	for _, tc := range cases {
		got := relativeAgo(now.Add(-tc.d))
		if got != tc.want {
			t.Errorf("%v ago: got %q want %q", tc.d, got, tc.want)
		}
	}
	if got := relativeAgo(time.Time{}); got != "" {
		t.Errorf("zero time: got %q", got)
	}
}
