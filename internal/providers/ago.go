package providers

import (
	"fmt"
	"time"
)

// relativeAgo is a short recency label for listings: "just now", "5 mins ago",
// "15 hours ago", "3 days ago". Older than a month falls back to the date.
func relativeAgo(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	now := time.Now()
	if t.After(now) {
		t = now
	}
	d := now.Sub(t)
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		return "1 min ago"
	case d < time.Hour:
		return fmt.Sprintf("%d mins ago", int(d/time.Minute))
	case d < 90*time.Minute:
		return "1 hour ago"
	case d < 24*time.Hour:
		hours := int(d / time.Hour)
		if hours <= 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 36*time.Hour:
		return "1 day ago"
	case d < 30*24*time.Hour:
		days := int(d / (24 * time.Hour))
		if days <= 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Local().Format("2006-01-02")
	}
}
