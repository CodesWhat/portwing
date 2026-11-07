// SPDX-License-Identifier: MIT

package server

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestRateLimiterWindowBoundary(t *testing.T) {
	t.Parallel()
	const ip = "192.0.2.1"
	for _, tc := range []struct {
		name    string
		record  func(*testing.T, *RateLimiter)
		limited func(*RateLimiter) bool
	}{
		{
			name:    "authentication failures",
			record:  func(_ *testing.T, rl *RateLimiter) { rl.RecordFailure(ip) },
			limited: func(rl *RateLimiter) bool { return rl.IsRateLimited(ip) },
		},
		{
			name:    "body abuse",
			record:  func(_ *testing.T, rl *RateLimiter) { rl.recordAbuse(ip) },
			limited: func(rl *RateLimiter) bool { return rl.isAbuseRateLimited(ip) },
		},
		{
			name: "reserved authentication",
			record: func(t *testing.T, rl *RateLimiter) {
				t.Helper()
				if !rl.tryBeginAuth(ip) {
					t.Fatal("authentication refused before failure limit")
				}
				rl.finishAuth(ip, false)
			},
			limited: func(rl *RateLimiter) bool {
				accepted := rl.tryBeginAuth(ip)
				if accepted {
					rl.finishAuth(ip, true)
				}
				return !accepted
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				rl := NewRateLimiter()
				defer rl.Stop()
				for i := 0; i < rl.maxFails-1; i++ {
					tc.record(t, rl)
				}
				time.Sleep(rl.window)
				tc.record(t, rl)
				if !tc.limited(rl) {
					t.Fatal("failure budget reset at exact window boundary")
				}
				rl.sweepExpired(time.Now())
				if !tc.limited(rl) {
					t.Fatal("sweep removed entry at exact window boundary")
				}
				time.Sleep(time.Nanosecond)
				if tc.limited(rl) {
					t.Fatal("failure budget still blocked after window expired")
				}
				tc.record(t, rl)
				if tc.limited(rl) {
					t.Fatal("new window inherited previous failure budget")
				}
			})
		})
	}
}
