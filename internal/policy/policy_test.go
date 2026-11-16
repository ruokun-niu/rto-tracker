// Copyright (c) 2026 Ruokun (Tommy) Niu
// Licensed under the MIT License. See LICENSE file in the project root for details.

package policy

import (
	"reflect"
	"testing"
	"time"

	"rto-tracker/internal/db"
)

func TestStartOfWeekSunday(t *testing.T) {
	tests := []struct {
		date string
		want string
	}{
		{"2026-11-08T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-09T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-10T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-11T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-12T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-13T15:30:00Z", "2026-11-08T00:00:00Z"},
		{"2026-11-14T23:59:59Z", "2026-11-08T00:00:00Z"},
		{"2027-01-01T12:00:00Z", "2026-12-27T00:00:00Z"},
		{"2026-11-08T00:30:00+10:00", "2026-11-08T00:00:00+10:00"},
		{"2026-11-14T23:30:00-08:00", "2026-11-08T00:00:00-08:00"},
	}
	for _, tt := range tests {
		t.Run(tt.date, func(t *testing.T) {
			date, err := time.Parse(time.RFC3339, tt.date)
			if err != nil {
				t.Fatal(err)
			}
			got := startOfWeek(date)
			if got.Format(time.RFC3339) != tt.want {
				t.Errorf("startOfWeek(%s) = %s, want %s", tt.date, got.Format(time.RFC3339), tt.want)
			}
			if got.Location() != date.Location() {
				t.Error("startOfWeek changed the time zone")
			}
		})
	}
}

func TestEvaluateAtSundaySaturdayWeeks(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	store, err := db.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, date := range []string{
		"2026-10-31", "2026-11-01", "2026-11-07",
		"2026-11-08", "2026-11-09", "2026-11-14", "2026-11-15",
	} {
		if err := store.BadgeIn(date); err != nil {
			t.Fatal(err)
		}
	}

	for _, policyType := range []string{"belt_average", "belt_strict", "straight"} {
		for _, date := range []string{"2026-11-08", "2026-11-14"} {
			t.Run(policyType+"/"+date, func(t *testing.T) {
				now, err := time.Parse("2006-01-02", date)
				if err != nil {
					t.Fatal(err)
				}
				pol := &db.PolicyConfig{
					PolicyType: policyType, WindowWeeks: 2, BestWeeks: 1, DaysPerWeek: 3,
				}
				status, err := EvaluateAt(store, pol, now)
				if err != nil {
					t.Fatal(err)
				}
				if status.WindowStart != "2026-11-01" || status.WindowEnd != "2026-11-14" {
					t.Errorf("window = %s through %s, want 2026-11-01 through 2026-11-14",
						status.WindowStart, status.WindowEnd)
				}
				straight := policyType == "straight"
				wantWeeks := []WeekDetail{
					{WeekStart: "2026-11-01", WeekEnd: "2026-11-07", Count: 2, Required: 3, IsBest: straight},
					{WeekStart: "2026-11-08", WeekEnd: "2026-11-14", Count: 3, Required: 3, IsBest: true},
				}
				if !reflect.DeepEqual(status.WeekSummaries, wantWeeks) {
					t.Errorf("weekly summaries = %+v, want %+v", status.WeekSummaries, wantWeeks)
				}
				wantTotal, wantRequired, wantNeeded := 3, 3, 0
				if straight {
					wantTotal, wantRequired, wantNeeded = 5, 6, 1
				}
				if status.TotalBadged != wantTotal || status.RequiredTotal != wantRequired ||
					status.DaysNeeded != wantNeeded || status.IsCompliant != !straight {
					t.Errorf("compliance = total %d, required %d, needed %d, compliant %t; want %d, %d, %d, %t",
						status.TotalBadged, status.RequiredTotal, status.DaysNeeded, status.IsCompliant,
						wantTotal, wantRequired, wantNeeded, !straight)
				}

				pol.StartDate = "2026-11-08"
				status, err = EvaluateAt(store, pol, now)
				if err != nil {
					t.Fatal(err)
				}
				if status.WindowStart != "2026-11-08" ||
					!reflect.DeepEqual(status.WeekSummaries, wantWeeks[1:]) {
					t.Errorf("policy starting Sunday: window start %s, weeks %+v; want 2026-11-08, %+v",
						status.WindowStart, status.WeekSummaries, wantWeeks[1:])
				}
			})
		}
	}
}
