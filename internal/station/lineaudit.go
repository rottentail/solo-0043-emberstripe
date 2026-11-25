package station

import (
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/lineaudit"
	"github.com/rottentail/solo-0043-emberstripe/internal/readout"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// LineAudit compares two registered reels without writing a parcel or report.
func (s *Station) LineAudit(req lineaudit.Request) (*lineaudit.Report, error) {
	a, ok := s.Reel(req.A)
	if !ok {
		return nil, fmt.Errorf("unknown A scan %q", req.A)
	}
	b, ok := s.Reel(req.B)
	if !ok {
		return nil, fmt.Errorf("unknown B scan %q", req.B)
	}
	return lineaudit.Compare(a, b, req)
}

// RowAudition renders an arbitrary row interval for click-through conclusions.
func RowAudition(reel *capture.Reel, profile string, start, end int, sourceClock bool) ([]byte, int, error) {
	total := reel.Rows * reel.Frames
	limit := register.MaxFrames * reel.Rows
	if start < 0 || end <= start || end > total || end-start > limit {
		return nil, 0, fmt.Errorf("expected 0 <= start_row < end_row <= %d and at most %d rows", total, limit)
	}
	g, err := gauge.Measure(reel, profile)
	if err != nil {
		if profile != "" {
			return nil, 0, err
		}
		// Alignment can run on raw lane contours even when no gauge patch is
		// usable. Give row audition a monotonic 0..65535 mapping in that case.
		g = gauge.Gauge{Profile: "raw-gray", Lanes: [2]gauge.Range{{Dark: 0, Clear: 65535}, {Dark: 0, Clear: 65535}}}
	}
	samples := readout.ExtractRows(reel, g, start, end, sourceClock)
	return wave.Encode(register.Rate(reel.Header), samples), register.Rate(reel.Header), nil
}
