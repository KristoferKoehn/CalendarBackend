package validation

import (
	"strings"
	"testing"
)

func TestEmail(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"User@Example.COM", "user@example.com", true},
		{"  a@b.co  ", "a@b.co", true},
		{"a@b.c", "a@b.c", true},
		{"not-an-email", "", false},
		{"a@b", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := Email(c.in)
		if c.ok && err != nil {
			t.Fatalf("Email(%q) unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("Email(%q) expected error", c.in)
		}
		if c.ok && got != c.want {
			t.Fatalf("Email(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTimeRange(t *testing.T) {
	cases := []struct {
		start, end string
		ok         bool
	}{
		{"14:00", "15:00", true},
		{"15:00", "14:00", false},
		{"14:00", "14:00", false},
		{"23:59", "24:00", false},
	}
	for _, c := range cases {
		err := TimeRange(c.start, c.end)
		if c.ok && err != nil {
			t.Fatalf("TimeRange(%q,%q) unexpected error: %v", c.start, c.end, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("TimeRange(%q,%q) expected error", c.start, c.end)
		}
	}
}

func TestDateAndTime(t *testing.T) {
	// Date checks format only, mirroring the Zod schema. Month 13 passes the
	// format check even though it is not a calendar date.
	for _, d := range []string{"2026-10-10", "0000-01-01", "2026-13-01"} {
		if err := Date(d); err != nil {
			t.Fatalf("Date(%q) unexpected error: %v", d, err)
		}
	}
	for _, d := range []string{"2026-1-1", "10/10/2026", "20261010", ""} {
		if err := Date(d); err == nil {
			t.Fatalf("Date(%q) expected error", d)
		}
	}
	for _, v := range []string{"00:00", "09:30", "23:59"} {
		if err := Time(v); err != nil {
			t.Fatalf("Time(%q) unexpected error: %v", v, err)
		}
	}
	for _, v := range []string{"24:00", "9:30", "12:60", ""} {
		if err := Time(v); err == nil {
			t.Fatalf("Time(%q) expected error", v)
		}
	}
}

func TestTitle(t *testing.T) {
	long := strings.Repeat("a", 201)
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Standup", "Standup", true},
		{"  Standup  ", "Standup", true},
		{"", "", false},
		{"   ", "", false},
		{long, "", false},
	}
	for _, c := range cases {
		got, err := Title(c.in)
		if c.ok && err != nil {
			t.Fatalf("Title(%q) unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("Title(%q) expected error", c.in)
		}
		if c.ok && got != c.want {
			t.Fatalf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDescription(t *testing.T) {
	long := strings.Repeat("a", 2001)
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"Daily sync", "Daily sync", true},
		{"  Daily sync  ", "Daily sync", true},
		{long, "", false},
	}
	for _, c := range cases {
		got, err := Description(c.in)
		if c.ok && err != nil {
			t.Fatalf("Description(%q) unexpected error: %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("Description(%q) expected error", c.in)
		}
		if c.ok && got != c.want {
			t.Fatalf("Description(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
