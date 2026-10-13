package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// testLimiter returns a limiter driven by a clock the test controls.
func testLimiter(t *testing.T, now *time.Time) *Limiter {
	t.Helper()
	l := newWithClock(time.Hour, func() time.Time { return *now })
	t.Cleanup(l.Close)
	return l
}

func TestAllowPermitsUntilLimitReached(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)
	limit := Limit{Failures: 3, Window: 15 * time.Minute}

	for i := 0; i < 3; i++ {
		if allowed, _ := l.Allow("k", limit); !allowed {
			t.Fatalf("attempt %d should have been allowed", i+1)
		}
		l.Fail("k")
	}

	allowed, retry := l.Allow("k", limit)
	if allowed {
		t.Fatal("the fourth attempt should have been refused")
	}
	if retry <= 0 || retry > limit.Window {
		t.Fatalf("retry hint out of range: %v", retry)
	}
}

func TestBlockClearsOnceTheWindowPasses(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)
	limit := Limit{Failures: 2, Window: 15 * time.Minute}

	l.Fail("k")
	l.Fail("k")
	if allowed, _ := l.Allow("k", limit); allowed {
		t.Fatal("expected to be blocked")
	}

	// One second short of the window, still blocked.
	now = now.Add(15*time.Minute - time.Second)
	if allowed, _ := l.Allow("k", limit); allowed {
		t.Fatal("expected to still be blocked just before the window elapsed")
	}

	// Past the window, allowed again.
	now = now.Add(2 * time.Second)
	if allowed, _ := l.Allow("k", limit); !allowed {
		t.Fatal("expected the block to have lifted")
	}
}

func TestResetClearsFailures(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)
	limit := Limit{Failures: 1, Window: 15 * time.Minute}

	l.Fail("k")
	if allowed, _ := l.Allow("k", limit); allowed {
		t.Fatal("expected to be blocked")
	}

	// A successful sign-in resets the counter, so a mistyped password does not
	// leave someone throttled.
	l.Reset("k")
	if allowed, _ := l.Allow("k", limit); !allowed {
		t.Fatal("expected the reset to clear the block")
	}
}

func TestKeysAreIndependent(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)
	limit := Limit{Failures: 1, Window: 15 * time.Minute}

	l.Fail("a")
	if allowed, _ := l.Allow("a", limit); allowed {
		t.Fatal("key a should be blocked")
	}
	if allowed, _ := l.Allow("b", limit); !allowed {
		t.Fatal("key b should be unaffected by key a")
	}
}

func TestDisabledLimitAllowsEverything(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)

	for i := 0; i < 100; i++ {
		l.Fail("k")
	}
	if allowed, _ := l.Allow("k", Limit{Failures: 0, Window: time.Minute}); !allowed {
		t.Fatal("a zero-failure limit should disable throttling")
	}
	if allowed, _ := l.Allow("k", Limit{Failures: 5, Window: 0}); !allowed {
		t.Fatal("a zero window should disable throttling")
	}
}

func TestSweepDropsStaleKeys(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	l := testLimiter(t, &now)

	l.Fail("old")
	if l.Size() != 1 {
		t.Fatalf("expected 1 tracked key, got %d", l.Size())
	}

	// Within the prune interval, the key is retained even though it is idle.
	now = now.Add(30 * time.Minute)
	l.Sweep()
	if l.Size() != 1 {
		t.Fatalf("expected the key to be retained inside the prune interval, got %d", l.Size())
	}

	// Well past it, the key is collected so the map cannot grow without bound.
	now = now.Add(3 * time.Hour)
	l.Sweep()
	if l.Size() != 0 {
		t.Fatalf("expected stale keys to be swept, got %d", l.Size())
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		fwd        string
		real       string
		trust      bool
		want       string
	}{
		{name: "remote addr is used by default", remoteAddr: "203.0.113.7:54321", want: "203.0.113.7"},
		{
			name:       "forwarded header ignored unless trusted",
			remoteAddr: "203.0.113.7:54321",
			fwd:        "198.51.100.9",
			trust:      false,
			want:       "203.0.113.7",
		},
		{
			name:       "forwarded header honoured when trusted",
			remoteAddr: "10.0.0.1:54321",
			fwd:        "198.51.100.9",
			trust:      true,
			want:       "198.51.100.9",
		},
		{
			name:       "left-most forwarded entry wins",
			remoteAddr: "10.0.0.1:54321",
			fwd:        "198.51.100.9, 10.0.0.5, 10.0.0.6",
			trust:      true,
			want:       "198.51.100.9",
		},
		{
			name:       "real-ip is the fallback",
			remoteAddr: "10.0.0.1:54321",
			real:       "198.51.100.20",
			trust:      true,
			want:       "198.51.100.20",
		},
		{
			name:       "unparseable remote addr is passed through",
			remoteAddr: "not-an-address",
			want:       "not-an-address",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
			r.RemoteAddr = tc.remoteAddr
			if tc.fwd != "" {
				r.Header.Set("X-Forwarded-For", tc.fwd)
			}
			if tc.real != "" {
				r.Header.Set("X-Real-IP", tc.real)
			}
			if got := ClientIP(r, tc.trust); got != tc.want {
				t.Fatalf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
