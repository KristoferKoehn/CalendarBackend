package timeutil

import (
	"errors"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		date    string
		wall    string
		wantUTC string
		wantErr error
	}{
		{
			name:    "PDT afternoon",
			date:    "2026-10-10",
			wall:    "14:00",
			wantUTC: "2026-10-10T21:00:00Z",
		},
		{
			name:    "PST winter",
			date:    "2026-01-15",
			wall:    "14:00",
			wantUTC: "2026-01-15T22:00:00Z",
		},
		{
			name:    "spring forward gap 02:30",
			date:    "2026-03-08",
			wall:    "02:30",
			wantErr: ErrNonexistent,
		},
		{
			name:    "spring forward gap 02:00",
			date:    "2026-03-08",
			wall:    "02:00",
			wantErr: ErrNonexistent,
		},
		{
			name:    "fall back ambiguous 01:30",
			date:    "2026-11-01",
			wall:    "01:30",
			wantErr: ErrAmbiguous,
		},
		{
			name:    "fall back ambiguous 01:00",
			date:    "2026-11-01",
			wall:    "01:00",
			wantErr: ErrAmbiguous,
		},
		{
			name:    "valid before spring gap",
			date:    "2026-03-08",
			wall:    "01:30",
			wantUTC: "2026-03-08T09:30:00Z",
		},
		{
			name:    "valid after spring gap",
			date:    "2026-03-08",
			wall:    "03:30",
			wantUTC: "2026-03-08T10:30:00Z",
		},
		{
			name:    "valid after fall back",
			date:    "2026-11-01",
			wall:    "02:00",
			wantUTC: "2026-11-01T10:00:00Z",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Resolve(c.date, c.wall)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("Resolve(%s,%s) error = %v, want %v", c.date, c.wall, err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%s,%s) unexpected error: %v", c.date, c.wall, err)
			}
			want, _ := time.Parse(time.RFC3339, c.wantUTC)
			if !got.Equal(want) {
				t.Fatalf("Resolve(%s,%s) = %s, want %s", c.date, c.wall, got, want)
			}
		})
	}
}

func TestLocalISO(t *testing.T) {
	utc, err := Resolve("2026-10-10", "14:00")
	if err != nil {
		t.Fatal(err)
	}
	if got := LocalISO("2026-10-10", "14:00", utc); got != "2026-10-10T14:00:00-07:00" {
		t.Fatalf("LocalISO summer = %q, want -07:00", got)
	}

	winter, err := Resolve("2026-01-15", "14:00")
	if err != nil {
		t.Fatal(err)
	}
	if got := LocalISO("2026-01-15", "14:00", winter); got != "2026-01-15T14:00:00-08:00" {
		t.Fatalf("LocalISO winter = %q, want -08:00", got)
	}
}

func TestFormatLocal(t *testing.T) {
	utc, _ := time.Parse(time.RFC3339, "2026-10-10T21:00:00Z")
	if got := FormatLocal(utc); got != "2026-10-10T14:00:00-07:00" {
		t.Fatalf("FormatLocal = %q, want -07:00", got)
	}

	winter, _ := time.Parse(time.RFC3339, "2026-01-15T22:00:00Z")
	if got := FormatLocal(winter); got != "2026-01-15T14:00:00-08:00" {
		t.Fatalf("FormatLocal winter = %q, want -08:00", got)
	}
}

func TestDayBounds(t *testing.T) {
	cases := []struct {
		name    string
		date    string
		start   string
		nextDay string
	}{
		{"regular day", "2026-10-10", "2026-10-10T07:00:00Z", "2026-10-11T07:00:00Z"},
		{"spring forward day", "2026-03-08", "2026-03-08T08:00:00Z", "2026-03-09T07:00:00Z"},
		{"fall back day", "2026-11-01", "2026-11-01T07:00:00Z", "2026-11-02T08:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			start, err := DayStart(c.date)
			if err != nil {
				t.Fatal(err)
			}
			if got := start.Format(time.RFC3339); got != c.start {
				t.Fatalf("DayStart(%s) = %s, want %s", c.date, got, c.start)
			}

			end, err := NextDayStart(c.date)
			if err != nil {
				t.Fatal(err)
			}
			if got := end.Format(time.RFC3339); got != c.nextDay {
				t.Fatalf("NextDayStart(%s) = %s, want %s", c.date, got, c.nextDay)
			}
		})
	}
}

func TestResolveRange(t *testing.T) {
	start, end, err := ResolveRange("2026-10-10", "14:00", "15:00")
	if err != nil {
		t.Fatal(err)
	}
	if !start.Before(end) {
		t.Fatal("start should be before end")
	}

	if _, _, err := ResolveRange("2026-10-10", "15:00", "14:00"); err == nil {
		t.Fatal("expected error for end <= start")
	}
}
