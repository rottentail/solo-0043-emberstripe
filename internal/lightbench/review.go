package lightbench

import (
	"net/http"
	"sort"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

const (
	boundarySound   = "sound"
	boundaryPicture = "picture"
)

// reviewBoundary marks the first picture row whose status differs from the
// row before it. Sound boundaries carry the two adjacent coverage states;
// picture boundaries carry the sheet depths on either side of the cut.
type reviewBoundary struct {
	Row       int    `json:"row"`
	Frame     int    `json:"frame"`
	Kinds     string `json:"kinds"`
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	FromDepth int    `json:"from_depth"`
	ToDepth   int    `json:"to_depth"`
}

type reviewReadout struct {
	Window     register.Window  `json:"window"`
	Rows       int              `json:"rows_per_frame"`
	Profile    gauge.Gauge      `json:"profile"`
	Boundaries []reviewBoundary `json:"boundaries"`
}

// reviewBoundaries derives sound status cuts from the merged coverage runs
// and picture presence/depth cuts at frame seams. A row carrying both
// keeps a single entry with kinds "sound,picture".
func reviewBoundaries(reel *capture.Reel, coverage []register.Run, rows, start, end int) []reviewBoundary {
	bounds := make(map[int]*reviewBoundary)
	order := make([]int, 0)

	ensure := func(row int) *reviewBoundary {
		if bound, ok := bounds[row]; ok {
			return bound
		}
		bound := &reviewBoundary{Row: row, Frame: row / rows}
		bounds[row] = bound
		order = append(order, row)
		return bound
	}

	// Sound cuts: the first row of every coverage run after the window head.
	for _, run := range coverage {
		if run.Start == start*rows {
			continue
		}
		bound := ensure(run.Start)
		bound.Kinds = mergeKinds(bound.Kinds, boundarySound)
		bound.From, bound.To = previousStatus(coverage, run.Start), run.Status
	}

	// Picture cuts: the first row of every frame whose sheet state changes.
	previousDepth := sheetDepth(reel, start)
	for frame := start + 1; frame < end; frame++ {
		depth := sheetDepth(reel, frame)
		if depth != previousDepth {
			bound := ensure(frame * rows)
			bound.Kinds = mergeKinds(bound.Kinds, boundaryPicture)
			bound.FromDepth, bound.ToDepth = previousDepth, depth
			previousDepth = depth
		}
	}

	sort.Ints(order)
	result := make([]reviewBoundary, 0, len(order))
	for _, row := range order {
		result = append(result, *bounds[row])
	}
	return result
}

func sheetDepth(reel *capture.Reel, frame int) int {
	if sheet, ok := reel.Sheets[frame]; ok {
		return sheet.Depth
	}
	return 0
}

func previousStatus(coverage []register.Run, row int) string {
	for index := range coverage {
		if coverage[index].Start == row && index > 0 {
			return coverage[index-1].Status
		}
	}
	return ""
}

func mergeKinds(existing, kind string) string {
	if existing == "" {
		return kind
	}
	return existing + "," + kind
}

func (b *bench) review(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	window, ok := requestedWindow(r, reel.Header)
	if !ok {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	calibration, render, err := station.RenderWindow(reel, window, r.URL.Query().Get("profile"))
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	writeJSON(w, http.StatusOK, reviewReadout{
		Window:     window,
		Rows:       reel.Rows,
		Profile:    calibration,
		Boundaries: reviewBoundaries(reel, render.Coverage, reel.Rows, window.Start, window.End),
	})
}
