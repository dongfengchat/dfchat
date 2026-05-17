package auth

import "testing"

// TestIsLockedPattern locks in the user's definitive premium rule
// (尾数 4 个或以上一样). If a future change to isLockedPattern fails
// to match this table, this test goes red — preventing accidental
// drift from what the user actually wants.
//
// Mirror the DB trigger in migration 000022; both layers must agree.
func TestIsLockedPattern(t *testing.T) {
	cases := []struct {
		n      int64
		locked bool
		why    string
	}{
		// Segment 1 — all 10 expected premiums
		{100000, true, "5 trailing zeros"},
		{101111, true, "4 trailing 1s"},
		{102222, true, "4 trailing 2s"},
		{103333, true, "4 trailing 3s"},
		{104444, true, "4 trailing 4s"},
		{105555, true, "4 trailing 5s"},
		{106666, true, "4 trailing 6s"},
		{107777, true, "4 trailing 7s"},
		{108888, true, "4 trailing 8s"},
		{109999, true, "4 trailing 9s"},

		// Segment 2 / future segments — same shape
		{110000, true, "4 trailing zeros, segment 2"},
		{111111, true, "6 same — trivially passes tail rule"},
		{119999, true, "4 trailing 9s in segment 2"},
		{208888, true, "4 trailing 8s in segment 11"},
		{200888, false, "only 3 trailing 8s preceded by a 0"},

		// Patterns user explicitly called 垃圾号 — must NOT be locked
		{101101, false, "palindrome but tail = 1101 not all same"},
		{102201, false, "palindrome"},
		{109901, false, "palindrome"},
		{123456, false, "strict ascending — not premium per user spec"},
		{122220, false, "4 same in MIDDLE but tail = 2220 not all same"},
		{100001, false, "4 zeros in middle but tail = 0001 not all same"},

		// Ordinary numbers — must not be locked
		{105678, false, "random"},
		{100123, false, "random"},
	}
	for _, tc := range cases {
		got := isLockedPattern(tc.n)
		if got != tc.locked {
			t.Errorf("isLockedPattern(%d) = %v, want %v (%s)",
				tc.n, got, tc.locked, tc.why)
		}
	}
}

// TestIsLockedPattern_SegmentCounts validates the per-10k-segment
// volume invariant: exactly 10 premium numbers per segment (one for
// each trailing digit 0..9). If this ever drifts away from 10, either
// the rule changed or it has a bug.
func TestIsLockedPattern_SegmentCounts(t *testing.T) {
	// Sample three segments to be safe.
	for _, start := range []int64{100000, 110000, 250000} {
		end := start + 9999
		count := 0
		for n := start; n <= end; n++ {
			if isLockedPattern(n) {
				count++
			}
		}
		if count != 10 {
			t.Errorf("segment %d-%d: got %d premium, want 10", start, end, count)
		}
	}
}
