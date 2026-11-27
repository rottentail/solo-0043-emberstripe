// Package transfer checks whether calibration endpoints can be migrated between
// two scanners that share intermediate reference strips.
//
// The reference reel keeps its full dark/clear calibration. The target reel has
// lost part of its own reference boards, so its scanner response can only be
// observed on the intermediate strips both devices can read. On every shared
// strip the two devices measure the same physical optical level, which lets a
// linear response map be fitted independently for each lane. The reference
// dark/clear endpoints are then mapped into the target's gray scale and only
// accepted when the extrapolation stays ordered and inside the 16-bit range.
package transfer

import (
	"errors"
	"fmt"
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/gauge"
)

// minStrips is the smallest shared-strip count that fixes slope and intercept.
const minStrips = 2

// contrastFloor mirrors gauge.Measure: a usable endpoint pair must span at
// least 1024 gray levels on the target device.
const contrastFloor = 1024

const (
	// StatusOK marks a lane whose endpoints were migrated and pass all checks.
	StatusOK = "transferable"
	// StatusShort marks a lane with too few shared strips to fit a response map.
	StatusShort = "insufficient_strips"
	// StatusUnordered marks a lane whose fitted endpoints reverse or collapse.
	StatusUnordered = "reversed_endpoints"
	// StatusOutOfRange marks a lane whose fitted endpoints leave the 16-bit scale.
	StatusOutOfRange = "endpoint_out_of_range"
)

// StripPatch is one reference rectangle that makes up a shared strip.
type StripPatch struct {
	Frame int `json:"frame"`
	Row0  int `json:"row_start"`
	Row1  int `json:"row_end"`
}

// Strip is one intermediate reference strip observed on both scanners. Its
// gray level is the equal-weight mean of every declared patch rectangle.
type Strip struct {
	ID        string        `json:"id"`
	Patches   [2]StripPatch `json:"patches"`
	Reference float64       `json:"reference_gray"`
	Target    float64       `json:"target_gray"`
}

// Fit is the linear target = Slope*reference + Intercept response map.
type Fit struct {
	Slope     float64 `json:"slope"`
	Intercept float64 `json:"intercept"`
}

// LaneVerdict reports one lane's migration decision with its full evidence.
type LaneVerdict struct {
	Lane     int          `json:"lane"`
	Status   string       `json:"status"`
	Reason   string       `json:"reason,omitempty"`
	Strips   []Strip      `json:"strips"`
	Fit      *Fit         `json:"fit,omitempty"`
	Native   *gauge.Range `json:"native_endpoints,omitempty"`
	Migrated *gauge.Range `json:"migrated_endpoints,omitempty"`
	Contrast float64      `json:"contrast,omitempty"`
}

// Check is the complete, reviewable transfer report for both lanes.
type Check struct {
	Target          string         `json:"target"`
	Reference       string         `json:"reference"`
	ReferenceID     string         `json:"reference_profile"`
	Lanes           [2]LaneVerdict `json:"lanes"`
	Transferable    [2]bool        `json:"transferable"`
	UsableLanes     int            `json:"usable_lanes"`
	GeometryAligned bool           `json:"geometry_aligned"`
}

// profileByID locates a declared strip profile by its id.
func profileByID(reel *capture.Reel, id string) (capture.Profile, bool) {
	for _, profile := range reel.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return capture.Profile{}, false
}

func asPatch(patch capture.Patch) StripPatch {
	return StripPatch{Frame: patch.Frame, Row0: patch.Rows.X0, Row1: patch.Rows.X1}
}

// samePatchSet requires both reels to declare the strip at the same physical
// rectangles so the two devices observe the same reference area.
func samePatchSet(a, b capture.Profile) bool {
	return a.Dark.Frame == b.Dark.Frame && a.Dark.Rows == b.Dark.Rows &&
		a.Clear.Frame == b.Clear.Frame && a.Clear.Rows == b.Clear.Rows
}

// stripMean integrates all of a strip's patch rectangles on one lane. A
// missing sheet on any patch makes the strip unobservable on that device.
func stripMean(reel *capture.Reel, lane int, profile capture.Profile) (float64, bool) {
	var total, pixels float64
	for _, patch := range []capture.Patch{profile.Dark, profile.Clear} {
		sheet, ok := reel.Sheets[patch.Frame]
		if !ok {
			return 0, false
		}
		width := reel.Lanes[lane].Width()
		height := patch.Rows.Width()
		total += gauge.Mean(sheet, reel.Lanes[lane], patch.Rows) * float64(width*height)
		pixels += float64(width * height)
	}
	return total / pixels, true
}

// GeometryCompatible requires the same clock so gray levels from the two
// devices refer to the same physical rows and frame cadence.
func GeometryCompatible(target, reference *capture.Reel) error {
	if target.Rows != reference.Rows || target.FPS != reference.FPS || target.Frames != reference.Frames || target.Width != reference.Width {
		return errors.New("two captures must share width, frame count, rows per frame and frame rate")
	}
	for lane := range target.Lanes {
		if target.Lanes[lane] != reference.Lanes[lane] {
			return fmt.Errorf("lane %d column spans differ between captures", lane)
		}
	}
	return nil
}

// collectStrips gathers the intermediate strips both devices can read on a lane.
func collectStrips(target, reference *capture.Reel, lane int, ids []string) ([]Strip, []string) {
	strips := make([]Strip, 0, len(ids))
	skipped := make([]string, 0)
	for _, id := range ids {
		targetProfile, ok := profileByID(target, id)
		if !ok {
			skipped = append(skipped, id+": 当前卷未声明该试条")
			continue
		}
		refProfile, ok := profileByID(reference, id)
		if !ok {
			skipped = append(skipped, id+": 参考卷未声明该试条")
			continue
		}
		if !samePatchSet(targetProfile, refProfile) {
			skipped = append(skipped, id+": 两卷试条位置不一致")
			continue
		}
		refMean, okRef := stripMean(reference, lane, refProfile)
		targetMean, okTarget := stripMean(target, lane, targetProfile)
		if !okRef || !okTarget {
			skipped = append(skipped, id+": 至少一台设备缺该试条页")
			continue
		}
		strips = append(strips, Strip{
			ID:        id,
			Patches:   [2]StripPatch{asPatch(targetProfile.Dark), asPatch(targetProfile.Clear)},
			Reference: refMean,
			Target:    targetMean,
		})
	}
	return strips, skipped
}

// leastSquares fits target = slope*reference + intercept. The two devices may
// be supplied in either order, so the fit direction follows the argument order
// rather than assuming which scanner has the wider response.
func leastSquares(strips []Strip) (Fit, error) {
	n := float64(len(strips))
	var sumX, sumY, sumXX, sumXY float64
	for _, strip := range strips {
		sumX += strip.Reference
		sumY += strip.Target
		sumXX += strip.Reference * strip.Reference
		sumXY += strip.Reference * strip.Target
	}
	denominator := n*sumXX - sumX*sumX
	if math.Abs(denominator) < 1e-9 {
		return Fit{}, errors.New("shared strips collapse to one gray level")
	}
	slope := (n*sumXY - sumX*sumY) / denominator
	intercept := (sumY - slope*sumX) / n
	return Fit{Slope: slope, Intercept: intercept}, nil
}

func (f Fit) apply(level float64) float64 { return f.Slope*level + f.Intercept }

// evaluateLane decides one lane independently of the other lane.
func evaluateLane(target, reference *capture.Reel, native gauge.Gauge, refGauge gauge.Gauge, lane int, ids []string) LaneVerdict {
	verdict := LaneVerdict{Lane: lane}
	if nativeRange, ok := nativeLane(target, lane); ok {
		verdict.Native = &nativeRange
	}
	strips, skipped := collectStrips(target, reference, lane, ids)
	verdict.Strips = strips
	if len(skipped) > 0 {
		verdict.Reason = joinReasons(skipped)
	}
	if len(strips) < minStrips {
		verdict.Status = StatusShort
		if verdict.Reason == "" {
			verdict.Reason = fmt.Sprintf("仅 %d 块共用试条，至少需要 %d 块", len(strips), minStrips)
		}
		return verdict
	}
	fit, err := leastSquares(strips)
	if err != nil {
		verdict.Status = StatusShort
		verdict.Reason = joinReasons(append(skipped, err.Error()))
		return verdict
	}
	verdict.Fit = &fit
	refEndpoints := refGauge.Lanes[lane]
	migrated := gauge.Range{Dark: fit.apply(refEndpoints.Dark), Clear: fit.apply(refEndpoints.Clear)}
	verdict.Migrated = &migrated
	verdict.Contrast = migrated.Clear - migrated.Dark
	switch {
	case fit.Slope <= 0 || migrated.Clear <= migrated.Dark:
		verdict.Status = StatusUnordered
		verdict.Reason = joinReasons(append(skipped, "拟合端点倒置或无对比度"))
	case migrated.Dark < 0 || migrated.Clear > 65535:
		verdict.Status = StatusOutOfRange
		verdict.Reason = joinReasons(append(skipped, "拟合端点越出 16 位灰度量程"))
	case verdict.Contrast < contrastFloor:
		verdict.Status = StatusUnordered
		verdict.Reason = joinReasons(append(skipped, fmt.Sprintf("迁移后对比度 %.0f 低于 %d", verdict.Contrast, contrastFloor)))
	default:
		verdict.Status = StatusOK
	}
	return verdict
}

// nativeLane reports the target's own measured endpoints when usable. A lane
// whose native calibration failed is still allowed to recover via transfer.
func nativeLane(reel *capture.Reel, lane int) (gauge.Range, bool) {
	if len(reel.Profiles) == 0 {
		return gauge.Range{}, false
	}
	native, err := gauge.Measure(reel, reel.Profiles[0].ID)
	if err != nil {
		return gauge.Range{}, false
	}
	return native.Lanes[lane], true
}

func joinReasons(parts []string) string {
	joined := ""
	for i, part := range parts {
		if i > 0 {
			joined += "；"
		}
		joined += part
	}
	return joined
}

// Evaluate fits a response map per lane and migrates the reference reel's
// dark/clear endpoints into the target reel's gray scale.
func Evaluate(target, reference *capture.Reel, referenceProfile string, stripIDs []string) (Check, error) {
	if referenceProfile == "" {
		return Check{}, errors.New("reference profile is required")
	}
	if len(stripIDs) == 0 {
		return Check{}, errors.New("at least one shared strip profile is required")
	}
	if err := GeometryCompatible(target, reference); err != nil {
		return Check{}, err
	}
	if seen := duplicateIDs(stripIDs); seen != "" {
		return Check{}, fmt.Errorf("shared strip %q listed more than once", seen)
	}
	refGauge, err := gauge.Measure(reference, referenceProfile)
	if err != nil {
		return Check{}, fmt.Errorf("reference calibration: %w", err)
	}
	// The target's own calibration may be broken; measure best-effort so the
	// report can still show native endpoints where they exist.
	native, _ := gauge.Measure(target, "")
	check := Check{
		Target: target.Name, Reference: reference.Name, ReferenceID: refGauge.Profile,
		GeometryAligned: true,
	}
	for lane := 0; lane < 2; lane++ {
		check.Lanes[lane] = evaluateLane(target, reference, native, refGauge, lane, stripIDs)
		check.Transferable[lane] = check.Lanes[lane].Status == StatusOK
		if check.Transferable[lane] {
			check.UsableLanes++
		}
	}
	return check, nil
}

func duplicateIDs(ids []string) string {
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return id
		}
		seen[id] = true
	}
	return ""
}

// MigratedGauge turns a transfer check into the gauge shape readout expects.
// Lanes that failed migration are muted instead of carrying invented endpoints.
func MigratedGauge(check Check) gauge.Gauge {
	g := gauge.Gauge{Profile: "transfer:" + check.Reference + ":" + check.ReferenceID}
	for lane, verdict := range check.Lanes {
		if verdict.Status == StatusOK && verdict.Migrated != nil {
			g.Lanes[lane] = *verdict.Migrated
		} else {
			g.Muted[lane] = true
		}
	}
	return g
}
