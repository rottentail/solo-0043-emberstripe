package lightbench

import "testing"

func TestCenteredSpan(t *testing.T) {
	cases := []struct {
		name            string
		row, span, limit int
		wantStart        int
		wantEnd          int
	}{
		{"centered", 400, 64, 2000, 368, 432},
		{"slides-in-at-start", 2, 16, 2000, 0, 16},
		{"clamps-to-zero", 0, 32, 2000, 0, 32},
		{"slides-in-at-end", 1999, 16, 2000, 1984, 2000},
		{"span-larger-than-limit", 3, 64, 32, 0, 32},
		{"single-row", 10, 1, 2000, 10, 11},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end := centeredSpan(tc.row, tc.span, tc.limit)
			if start != tc.wantStart || end != tc.wantEnd {
				t.Fatalf("centeredSpan(%d,%d,%d) = (%d,%d), want (%d,%d)",
					tc.row, tc.span, tc.limit, start, end, tc.wantStart, tc.wantEnd)
			}
			if start < 0 || end > tc.limit || start >= end {
				t.Fatalf("span (%d,%d) outside [0,%d) or empty", start, end, tc.limit)
			}
		})
	}
}
