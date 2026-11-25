package impactmap

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ParseSurvey decodes the documented survey schema and common field aliases.
// The aliases keep hand-authored and scripted payloads tolerant of page/frame
// and anchor/reference naming differences.
func ParseSurvey(data []byte) (Survey, error) {
	var plain surveyPlain
	if err := json.Unmarshal(data, &plain); err != nil {
		return Survey{}, invalid("invalid survey JSON: %w", err)
	}
	s := Survey(plain)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Survey{}, invalid("invalid survey JSON object: %w", err)
	}
	if s.Original == "" {
		s.Original = firstString(raw, "original", "original_reel", "originalReel", "original_scan", "originalScan", "old_reel", "old_scan", "source_reel", "sourceReel", "source", "primary", "first_reel", "a", "from_reel", "from")
	}
	if s.Rescan == "" {
		s.Rescan = firstString(raw, "rescan", "rescan_reel", "rescanReel", "rescan_scan", "rescanScan", "new_reel", "new_scan", "target_reel", "targetReel", "target", "candidate_reel", "supplement_reel", "supplemental_reel", "secondary", "backup", "second_reel", "b", "with")
	}
	if s.Profile == "" {
		s.Profile = firstString(raw, "profile", "calibration", "gauge")
	}
	if !s.Extrapolate {
		s.Extrapolate = firstBool(raw, "extrapolate", "allow_extrapolation", "allowExtrapolation")
	}
	if len(s.References) == 0 {
		if err := decodeFirst(raw, &s.References, "references", "anchors", "reference_points", "reference_positions", "control_points", "tie_points", "ties", "matches", "correspondences", "pairs"); err != nil {
			return s, err
		}
	}
	if len(s.Marks) == 0 {
		if err := decodeFirst(raw, &s.Marks, "marks", "segments", "tears", "tear_segments", "damage", "damage_segments", "damage_ranges", "circled_segments", "circles", "annotations", "injuries", "wounds"); err != nil {
			return s, err
		}
	}
	if len(s.Gaps) == 0 {
		if err := decodeFirst(raw, &s.Gaps, "gaps", "unscanned", "unscanned_ranges", "coverage_gaps", "missing_ranges", "missing"); err != nil {
			return s, err
		}
	}
	if s.Original == "" || s.Rescan == "" {
		return s, invalid("original and rescan reel names are required")
	}
	return s, nil
}

type surveyPlain Survey

func (s *Survey) UnmarshalJSON(data []byte) error {
	parsed, err := ParseSurvey(data)
	if err != nil {
		return err
	}
	*s = parsed
	return nil
}

func (p *Point) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var xs []int
		if err := json.Unmarshal(data, &xs); err != nil {
			return err
		}
		if len(xs) == 1 {
			p.Absolute = &xs[0]
			return nil
		}
		if len(xs) != 2 {
			return invalid("point array must be [page,row] or [absolute_row]")
		}
		p.Page, p.Row = xs[0], xs[1]
		return nil
	}
	if strings.HasPrefix(trimmed, "-") || (len(trimmed) > 0 && trimmed[0] >= '0' && trimmed[0] <= '9') {
		var abs int
		if err := json.Unmarshal(data, &abs); err != nil {
			return err
		}
		p.Absolute = &abs
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if abs := firstIntPtr(raw, "absolute_row", "absoluteRow", "absolute", "abs", "scan_row", "scanRow"); abs != nil {
		p.Absolute = abs
	}
	p.Page = firstInt(raw, 0, "page", "frame", "frame_number", "sheet", "sheet_number", "page_number")
	p.Row = firstInt(raw, 0, "row", "line", "line_number", "y", "row_index", "scan_row")
	return nil
}

func (r *Reference) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	r.ID = firstString(raw, "id", "name", "label")
	var err error
	if r.Original, err = pointFrom(raw, "original", "original_point", "original_position", "source", "from", "a", "first"); err != nil {
		return err
	}
	if isZeroPoint(r.Original) {
		if p, ok := prefixedPoint(raw, "original"); ok {
			r.Original = p
		} else if p, ok := prefixedPoint(raw, "source"); ok {
			r.Original = p
		}
	}
	if r.Rescan, err = pointFrom(raw, "rescan", "rescan_point", "rescan_position", "target", "to", "b", "second", "candidate"); err != nil {
		return err
	}
	if isZeroPoint(r.Rescan) {
		if p, ok := prefixedPoint(raw, "rescan"); ok {
			r.Rescan = p
		} else if p, ok := prefixedPoint(raw, "target"); ok {
			r.Rescan = p
		}
	}
	return nil
}

func (m *Mark) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	m.ID = firstString(raw, "id", "name", "mark_id")
	m.Note = firstString(raw, "note", "description", "label")
	m.Zone = firstString(raw, "zone", "area", "region", "kind_hint", "location", "position", "part")
	if m.Zone == "" && firstBool(raw, "sprocket_edge", "perforation_edge", "hole_edge", "near_sprocket", "near_perforation") {
		m.Zone = "edge"
	}
	if x0 := firstIntPtr(raw, "x0", "x_start", "xStart", "left", "column_start"); x0 != nil {
		m.X0 = x0
	}
	if x1 := firstIntPtr(raw, "x1", "x_end", "xEnd", "right", "column_end"); x1 != nil {
		m.X1 = x1
	}
	if _, ok := raw["columns"]; ok || raw["x"] != nil || raw["column_span"] != nil || raw["rect"] != nil || raw["bounds"] != nil || raw["box"] != nil {
		key := "columns"
		for _, candidate := range []string{"columns", "x", "column_span", "rect", "bounds", "box"} {
			if raw[candidate] != nil {
				key = candidate
				break
			}
		}
		var xs []int
		if err := json.Unmarshal(raw[key], &xs); err == nil && len(xs) == 2 {
			m.X0, m.X1 = &xs[0], &xs[1]
		} else {
			var sp struct {
				X0 *int `json:"x0"`
				X1 *int `json:"x1"`
			}
			_ = json.Unmarshal(raw[key], &sp)
			if sp.X0 != nil {
				m.X0 = sp.X0
			}
			if sp.X1 != nil {
				m.X1 = sp.X1
			}
		}
	}
	var intLanes []int
	if err := decodeFirst(raw, &intLanes, "lanes", "sound_lanes", "audio_lanes", "channels", "lane"); err == nil {
		m.Lanes = append(m.Lanes, intLanes...)
	} else {
		// A failed integer decode can have appended a zero before encountering
		// a lane name, so reset before parsing string labels.
		m.Lanes = nil
		var names []string
		if err := decodeFirst(raw, &names, "lanes", "sound_lanes", "audio_lanes", "channels", "lane"); err == nil {
			for _, name := range names {
				switch strings.ToLower(strings.TrimSpace(name)) {
				case "0", "l", "left", "左", "左声槽":
					m.Lanes = append(m.Lanes, 0)
				case "1", "r", "right", "右", "右声槽":
					m.Lanes = append(m.Lanes, 1)
				}
			}
		}
	}
	if len(m.Lanes) == 0 {
		switch strings.ToLower(firstString(raw, "lane", "lane_id", "channel")) {
		case "0", "l", "left", "左", "左声槽":
			m.Lanes = []int{0}
		case "1", "r", "right", "右", "右声槽":
			m.Lanes = []int{1}
		}
	}

	var err error
	if m.Start, err = pointFrom(raw, "start", "from", "begin", "a"); err != nil {
		return err
	}
	if m.End, err = pointFrom(raw, "end", "to", "finish", "b"); err != nil {
		return err
	}
	page := firstIntPtr(raw, "page", "frame", "sheet")
	startPage := firstIntPtr(raw, "start_page", "startPage", "from_page", "start_frame", "startFrame")
	endPage := firstIntPtr(raw, "end_page", "endPage", "to_page", "end_frame", "endFrame")
	startRow := firstIntPtr(raw, "start_row", "startRow", "row_start", "rowStart", "start_line", "line_start", "from_row", "y0", "y_start", "top")
	endRow := firstIntPtr(raw, "end_row", "endRow", "row_end", "rowEnd", "end_line", "line_end", "to_row", "y1", "y_end", "bottom")
	if startRow != nil && !pointExplicit(raw, "start", "from", "begin", "a") {
		m.Start.Row = *startRow
		switch {
		case startPage != nil:
			m.Start.Page = *startPage
		case page != nil:
			m.Start.Page = *page
		}
		m.Start.Absolute = nil
	}
	if endRow != nil && !pointExplicit(raw, "end", "to", "finish", "b") {
		m.End.Row = *endRow
		switch {
		case endPage != nil:
			m.End.Page = *endPage
		case page != nil:
			m.End.Page = *page
		case startPage != nil:
			m.End.Page = *startPage
		}
		m.End.Absolute = nil
	}
	return nil
}

func (g *Gap) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.Side = firstString(raw, "side", "scan", "copy")
	g.Reel = firstString(raw, "reel", "reel_name", "reelName", "capture")
	g.Reason = firstString(raw, "reason", "note")
	var err error
	if g.Start, err = pointFrom(raw, "start", "from", "begin"); err != nil {
		return err
	}
	if g.End, err = pointFrom(raw, "end", "to", "finish"); err != nil {
		return err
	}
	page := firstIntPtr(raw, "page", "frame", "sheet")
	startPage := firstIntPtr(raw, "start_page", "startPage", "from_page", "start_frame")
	if sr := firstIntPtr(raw, "start_row", "startRow", "row_start"); sr != nil && !pointExplicit(raw, "start", "from", "begin") {
		g.Start.Row = *sr
		switch {
		case startPage != nil:
			g.Start.Page = *startPage
		case page != nil:
			g.Start.Page = *page
		}
	}
	if er := firstIntPtr(raw, "end_row", "endRow", "row_end"); er != nil && !pointExplicit(raw, "end", "to", "finish") {
		g.End.Row = *er
		if ep := firstIntPtr(raw, "end_page", "endPage", "to_page"); ep != nil {
			g.End.Page = *ep
		} else if page != nil {
			g.End.Page = *page
		} else if startPage != nil {
			g.End.Page = *startPage
		}
	}
	return nil
}

func pointFrom(raw map[string]json.RawMessage, keys ...string) (Point, error) {
	for _, key := range keys {
		blob, ok := raw[key]
		if !ok || string(blob) == "null" {
			continue
		}
		var p Point
		if err := json.Unmarshal(blob, &p); err != nil {
			return Point{}, fmt.Errorf("%s: %w", key, err)
		}
		return p, nil
	}
	return Point{}, nil
}

func prefixedPoint(raw map[string]json.RawMessage, prefix string) (Point, bool) {
	pageKey := prefix + "_page"
	if raw[pageKey] == nil {
		pageKey = prefix + "_frame"
	}
	if raw[pageKey] == nil {
		return Point{}, false
	}
	return Point{
		Page: firstInt(raw, 0, pageKey),
		Row:  firstInt(raw, 0, prefix+"_row", prefix+"_start_row", prefix+"_line"),
	}, true
}

func decodeFirst(raw map[string]json.RawMessage, dst any, keys ...string) error {
	for _, key := range keys {
		blob, ok := raw[key]
		if !ok || string(blob) == "null" {
			continue
		}
		if err := json.Unmarshal(blob, dst); err != nil {
			return invalid("field %s: %w", key, err)
		}
		return nil
	}
	return nil
}

func firstString(raw map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		blob, ok := raw[key]
		if !ok {
			continue
		}
		var s string
		if err := json.Unmarshal(blob, &s); err == nil && s != "" {
			return s
		}
		var n json.Number
		if err := json.Unmarshal(blob, &n); err == nil {
			return n.String()
		}
	}
	return ""
}

func firstBool(raw map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		blob, ok := raw[key]
		if !ok {
			continue
		}
		var b bool
		if err := json.Unmarshal(blob, &b); err == nil {
			return b
		}
	}
	return false
}

func firstIntPtr(raw map[string]json.RawMessage, keys ...string) *int {
	for _, key := range keys {
		blob, ok := raw[key]
		if !ok {
			continue
		}
		var n int
		if err := json.Unmarshal(blob, &n); err == nil {
			return &n
		}
		var f float64
		if err := json.Unmarshal(blob, &f); err == nil {
			n = int(f)
			return &n
		}
	}
	return nil
}

func firstInt(raw map[string]json.RawMessage, def int, keys ...string) int {
	if p := firstIntPtr(raw, keys...); p != nil {
		return *p
	}
	return def
}

func pointExplicit(raw map[string]json.RawMessage, keys ...string) bool {
	for _, key := range keys {
		if blob, ok := raw[key]; ok && string(blob) != "null" {
			return true
		}
	}
	return false
}

func isZeroPoint(p Point) bool {
	return p.Absolute == nil && p.Page == 0 && p.Row == 0
}
