package recurrence

import (
	"testing"
	"time"
)

func TestNextDeadline(t *testing.T) {
	base := time.Date(2026, 3, 4, 18, 30, 0, 0, time.UTC) // среда

	cases := []struct {
		rule string
		want time.Time
	}{
		{"daily", base.AddDate(0, 0, 1)},
		{"weekly", base.AddDate(0, 0, 7)},
		{"biweekly", base.AddDate(0, 0, 14)},
		{"monthly", base.AddDate(0, 1, 0)},
		{"yearly", base.AddDate(1, 0, 0)},
	}
	// weekly/biweekly сдвигают на кратное 7 дням — день недели обязан сохраняться
	// (в этом смысл якоря в Spawn: закрыл в четверг задачу со средой — следующая
	// всё равно будет в среду). daily/monthly/yearly день недели не сохраняют.
	weekdayPreserved := map[string]bool{"weekly": true, "biweekly": true}

	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			got := NextDeadline(c.rule, base)
			if !got.Equal(c.want) {
				t.Errorf("NextDeadline(%q, base) = %v, want %v", c.rule, got, c.want)
			}
			if weekdayPreserved[c.rule] && got.Weekday() != time.Wednesday {
				t.Errorf("NextDeadline(%q, ...) weekday = %v, want Wednesday", c.rule, got.Weekday())
			}
			if got.Hour() != 18 || got.Minute() != 30 {
				t.Errorf("NextDeadline(%q, ...) time = %02d:%02d, want 18:30", c.rule, got.Hour(), got.Minute())
			}
		})
	}
}

func TestNextDeadline_UnknownRule(t *testing.T) {
	if got := NextDeadline("hourly", time.Now()); !got.IsZero() {
		t.Errorf("NextDeadline(unknown) = %v, want zero time", got)
	}
	if got := NextDeadline("", time.Now()); !got.IsZero() {
		t.Errorf("NextDeadline(\"\") = %v, want zero time", got)
	}
}

func TestIsValidRule(t *testing.T) {
	valid := []string{"daily", "weekly", "biweekly", "monthly", "yearly"}
	for _, r := range valid {
		if !IsValidRule(r) {
			t.Errorf("IsValidRule(%q) = false, want true", r)
		}
	}
	invalid := []string{"hourly", "", "DAILY", "once"}
	for _, r := range invalid {
		if IsValidRule(r) {
			t.Errorf("IsValidRule(%q) = true, want false", r)
		}
	}
}
