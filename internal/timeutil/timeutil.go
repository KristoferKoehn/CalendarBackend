// Package timeutil resolves America/Los_Angeles wall-clock times to UTC and
// formats the contract's local ISO fields.
package timeutil

import (
	"errors"
	"fmt"
	"time"

	_ "time/tzdata"
)

var (
	// ErrNonexistent marks a spring-forward gap time.
	ErrNonexistent = errors.New("local time does not exist (spring forward)")
	// ErrAmbiguous marks a fall-back overlap time.
	ErrAmbiguous = errors.New("local time is ambiguous (fall back)")
)

var location = loadLocation()

func loadLocation() *time.Location {
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		panic(err)
	}
	return loc
}

// Resolve returns the UTC instant for a Seattle wall-clock date and time.
// It rejects nonexistent (spring forward) and ambiguous (fall back) times.
func Resolve(date, wallTime string) (time.Time, error) {
	var y, mo, d, hh, mm int
	if _, err := fmt.Sscanf(date, "%d-%d-%d", &y, &mo, &d); err != nil {
		return time.Time{}, fmt.Errorf("parse date: %w", err)
	}
	if _, err := fmt.Sscanf(wallTime, "%d:%d", &hh, &mm); err != nil {
		return time.Time{}, fmt.Errorf("parse time: %w", err)
	}

	t := time.Date(y, time.Month(mo), d, hh, mm, 0, 0, location)

	wall := fmt.Sprintf("%02d:%02d", hh, mm)

	// Nonexistent (spring forward): the constructed instant renders as a
	// different wall clock than the input.
	if t.In(location).Format("15:04") != wall {
		return time.Time{}, ErrNonexistent
	}

	// Ambiguous (fall back): shifting one hour lands on the same wall clock
	// with a different offset.
	if sameWallDifferentOffset(t.Add(-time.Hour), t, wall) ||
		sameWallDifferentOffset(t.Add(time.Hour), t, wall) {
		return time.Time{}, ErrAmbiguous
	}

	return t.UTC(), nil
}

// ResolveRange resolves start and end and requires end strictly after start.
func ResolveRange(date, startTime, endTime string) (time.Time, time.Time, error) {
	start, err := Resolve(date, startTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := Resolve(date, endTime)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, errors.New("endTime must be after startTime")
	}
	return start, end, nil
}

// LocalISO formats the Seattle local ISO string with its UTC offset for the
// given instant, for example 2026-10-10T14:00:00-07:00.
func LocalISO(date, wallTime string, utc time.Time) string {
	offset := utc.In(location).Format("-07:00")
	return date + "T" + wallTime + ":00" + offset
}

func sameWallDifferentOffset(candidate, ref time.Time, wall string) bool {
	return candidate.In(location).Format("15:04") == wall &&
		candidate.In(location).Format("-07:00") != ref.In(location).Format("-07:00")
}
