// Package gauge measures optical reference marks independently per lane.
package gauge

import (
	"errors"
	"fmt"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
)

type Range struct {
	Dark  float64 `json:"dark"`
	Clear float64 `json:"clear"`
}
type Gauge struct {
	Profile string   `json:"profile"`
	Lanes   [2]Range `json:"lanes"`
	// Muted lanes have no trustworthy endpoints (for example a failed
	// cross-device transfer). Their samples stay at zero instead of being
	// normalized against invented dark/clear values.
	Muted [2]bool `json:"muted,omitempty"`
}

func Measure(reel *capture.Reel, id string) (Gauge, error) {
	if id == "" {
		id = reel.Profiles[0].ID
	}
	for _, profile := range reel.Profiles {
		if profile.ID != id {
			continue
		}
		dark, ok := reel.Sheets[profile.Dark.Frame]
		if !ok {
			return Gauge{}, errors.New("dark reference sheet missing")
		}
		clear, ok := reel.Sheets[profile.Clear.Frame]
		if !ok {
			return Gauge{}, errors.New("clear reference sheet missing")
		}
		g := Gauge{Profile: id}
		for i, lane := range reel.Lanes {
			lo := Mean(dark, lane, profile.Dark.Rows)
			hi := Mean(clear, lane, profile.Clear.Rows)
			if hi-lo < 1024 {
				return Gauge{}, fmt.Errorf("insufficient contrast in lane %d", i)
			}
			g.Lanes[i] = Range{Dark: lo, Clear: hi}
		}
		return g, nil
	}
	return Gauge{}, fmt.Errorf("unknown profile %q", id)
}

// Mean returns scanner-native intensities normalized to a common 16-bit scale.
func Mean(sheet capture.Sheet, columns, rows capture.Span) float64 {
	var total uint64
	for y := rows.X0; y < rows.X1; y++ {
		for x := columns.X0; x < columns.X1; x++ {
			total += uint64(sheet.Gray(x, y))
		}
	}
	return float64(total) / float64(columns.Width()*rows.Width())
}
