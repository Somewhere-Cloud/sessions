package main

import (
	"testing"
	"time"
)

func TestParseReturnTime(t *testing.T) {
	// Sunday 1 November 2026, 13:00 local.
	now := time.Date(2026, 11, 1, 13, 0, 0, 0, time.Local)
	cases := map[string]time.Time{
		"2h":                   now.Add(2 * time.Hour),
		"90m":                  now.Add(90 * time.Minute),
		"3d":                   now.Add(72 * time.Hour),
		"1d12h":                now.Add(36 * time.Hour),
		"18:00":                time.Date(2026, 11, 1, 18, 0, 0, 0, time.Local),
		"09:00":                time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local),
		"tomorrow":             time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local),
		"tomorrow 07:30":       time.Date(2026, 11, 2, 7, 30, 0, 0, time.Local),
		"mon 09:00":            time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local),
		"sunday":               time.Date(2026, 11, 8, 9, 0, 0, 0, time.Local),
		"2026-11-03 10:15":     time.Date(2026, 11, 3, 10, 15, 0, 0, time.Local),
		"2026-11-04T08:00:00Z": time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC),
	}
	for value, want := range cases {
		got, err := parseReturnTime(value, now)
		if err != nil || !got.Equal(want) {
			t.Errorf("parseReturnTime(%q) = %v, %v; want %v", value, got, err, want)
		}
	}
	for _, value := range []string{"soon", "25:00", "tomorrow at noon", "-2h"} {
		if _, err := parseReturnTime(value, now); err == nil {
			t.Errorf("parseReturnTime(%q) accepted", value)
		}
	}
}

func TestParseAsideArgsReadsATwoWordReturnTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 13, 0, 0, 0, time.Local)
	request, err := parseAsideArgs([]string{"0123abcd", "--until", "tomorrow", "09:00"}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local).UnixMilli()
	if len(request.targets) != 1 || request.until == nil || *request.until != want {
		t.Fatalf("request = %+v", request)
	}
	request, err = parseAsideArgs([]string{"0123abcd", "--until", "reset"}, now)
	if err != nil || !request.untilReset || request.until != nil {
		t.Fatalf("reset request = %+v, %v", request, err)
	}
	if _, err := parseAsideArgs([]string{"0123abcd", "--until", "2h", "--clear"}, now); err == nil {
		t.Fatal("--until with --clear was accepted")
	}
}

func TestReturnTimeLabel(t *testing.T) {
	now := time.Date(2026, 11, 1, 13, 0, 0, 0, time.Local)
	cases := map[time.Time]string{
		time.Date(2026, 11, 1, 18, 5, 0, 0, time.Local): "18:05",
		time.Date(2026, 11, 2, 9, 0, 0, 0, time.Local):  "tomorrow 09:00",
		time.Date(2026, 11, 4, 9, 0, 0, 0, time.Local):  "Wed 09:00",
		time.Date(2026, 12, 4, 9, 0, 0, 0, time.Local):  "Dec 4 09:00",
	}
	for at, want := range cases {
		if got := returnTimeLabel(at.UnixMilli(), now); got != want {
			t.Errorf("returnTimeLabel(%v) = %q, want %q", at, got, want)
		}
	}
}
