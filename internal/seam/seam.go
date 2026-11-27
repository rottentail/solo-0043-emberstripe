// Package seam inspects the boundaries between scanned picture pages.
//
// A page turn can carry a whole-page exposure change (the entire picture
// column block shifts in brightness while its structure stays put), a normal
// picture cut (the content itself changes), or no usable evidence (a missing
// sheet). The optical sound lanes live on their own offset clock, so every
// boundary also resolves the location that is actually audible, which can be a
// different source page than the picture boundary.
package seam

import (
	"math"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Decision labels attached to a picture-side page boundary.
const (
	ExposureShift = "exposure_shift" // 像整页曝光变动
	PictureCut    = "picture_cut"    // 像正常画面切换
	Inconclusive  = "inconclusive"   // 证据不足，无法判断
)

// Sound evidence labels describe whether the audible lanes change at all.
const (
	SoundStrong  = "shift"   // 声槽整体电平跳变
	SoundWeak    = "steady"  // 声槽没有明显跳变
	SoundMissing = "missing" // 相邻源页缺页，无声音证据
)

// Thresholds are expressed against the scanner-native 16-bit intensity scale.
const (
	// levelShift is the smallest page-mean step considered a real level change.
	levelShift = 0.08
	// lowStructure bounds the affine-fit residual fraction that still counts as
	// the same structure. Measured exposure shifts stay near zero while genuine
	// content cuts leave residuals well above this margin.
	lowStructure = 0.05
	// soundShift is the smallest lane-mean step reported as an audible jump.
	soundShift = 0.06
)

// Locator resolves a picture-clock row back to the source page that is read.
type Locator struct {
	PictureRow int               `json:"picture_row"`
	Frame      int               `json:"frame"`
	Row        int               `json:"row"`
	Status     string            `json:"status"`
	Source     *register.Address `json:"source,omitempty"`
}

// SoundSide records the level step heard when crossing the boundary, on the
// offset sound clock. Missing source pages leave the level fields at zero.
type SoundSide struct {
	Locator    Locator    `json:"heard_at"`
	Evidence   string     `json:"evidence"`
	LevelDelta [2]float64 `json:"level_delta"`
	LaneBefore [2]float64 `json:"lane_before"`
	LaneAfter  [2]float64 `json:"lane_after"`
}

// Seam is one boundary between two consecutive picture pages.
type Seam struct {
	AfterFrame int       `json:"after_frame"`
	PictureRow int       `json:"picture_row"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	LevelDelta float64   `json:"picture_level_delta"`
	Structure  float64   `json:"structure_delta"`
	PageBefore *PageStat `json:"page_before,omitempty"`
	PageAfter  *PageStat `json:"page_after,omitempty"`
	Picture    Locator   `json:"picture_locator"`
	Sound      SoundSide `json:"sound_locator"`
}

// PageStat summarizes one scanned page over the picture columns.
type PageStat struct {
	Frame  int     `json:"frame"`
	Mean   float64 `json:"mean"`
	Sigma  float64 `json:"sigma"`
	Exists bool    `json:"exists"`
	Depth  int     `json:"depth,omitempty"`
}

// Report covers every internal page boundary of a reel in picture order.
type Report struct {
	Reel   string         `json:"reel"`
	Rows   int            `json:"rows_per_frame"`
	Lead   int            `json:"lead_rows"`
	Rate   int            `json:"rate"`
	Seams  []Seam         `json:"seams"`
	Counts map[string]int `json:"counts"`
}

type pageProfile struct {
	stat     PageStat
	rowMean  []float64
	laneMean [2]float64
}

// Inspect walks every page boundary of the reel. Picture metrics use only the
// declared picture column block; sound metrics use the two optical lanes, so a
// change can appear on one side of the boundary and not the other.
func Inspect(reel *capture.Reel) Report {
	profiles := make([]pageProfile, reel.Frames)
	for frame := 0; frame < reel.Frames; frame++ {
		profiles[frame] = profilePage(reel, frame)
	}
	report := Report{
		Reel: reel.Name, Rows: reel.Rows, Lead: reel.Lead,
		Rate: register.Rate(reel.Header), Seams: []Seam{}, Counts: map[string]int{},
	}
	for frame := 1; frame < reel.Frames; frame++ {
		seam := Seam{AfterFrame: frame - 1, PictureRow: frame * reel.Rows}
		before, after := profiles[frame-1], profiles[frame]
		seam.PageBefore = present(before.stat)
		seam.PageAfter = present(after.stat)
		classify(&seam, before, after)
		seam.Picture = pictureLocator(reel, frame)
		seam.Sound = soundLocator(reel, frame, profiles)
		report.Counts[seam.Decision]++
		report.Seams = append(report.Seams, seam)
	}
	return report
}

func present(stat PageStat) *PageStat {
	if !stat.Exists {
		return nil
	}
	return &stat
}

// profilePage projects the picture columns onto per-row means and averages the
// two sound lanes over the whole page. Missing pages keep zeroed profiles so
// their boundaries remain listed but carry no numeric evidence.
func profilePage(reel *capture.Reel, frame int) pageProfile {
	profile := pageProfile{stat: PageStat{Frame: frame}}
	sheet, ok := reel.Sheets[frame]
	if !ok {
		return profile
	}
	profile.stat.Exists = true
	profile.stat.Depth = sheet.Depth
	rowMean := make([]float64, reel.Rows)
	pictureWidth := reel.Picture.Width()
	for row := 0; row < reel.Rows; row++ {
		var sum uint64
		for x := reel.Picture.X0; x < reel.Picture.X1; x++ {
			sum += uint64(sheet.Gray(x, row))
		}
		rowMean[row] = float64(sum) / float64(pictureWidth)
		profile.stat.Mean += rowMean[row]
	}
	profile.stat.Mean /= float64(reel.Rows)
	for _, value := range rowMean {
		dev := value - profile.stat.Mean
		profile.stat.Sigma += dev * dev
	}
	profile.stat.Sigma = math.Sqrt(profile.stat.Sigma / float64(reel.Rows))
	profile.rowMean = rowMean
	for lane, columns := range reel.Lanes {
		profile.laneMean[lane] = laneAverage(sheet, columns)
	}
	return profile
}

func laneAverage(sheet capture.Sheet, columns capture.Span) float64 {
	var total uint64
	for y := 0; y < sheet.Bitmap.Bounds().Dy(); y++ {
		for x := columns.X0; x < columns.X1; x++ {
			total += uint64(sheet.Gray(x, y))
		}
	}
	return float64(total) / float64(columns.Width()*sheet.Bitmap.Bounds().Dy())
}

// classify decides the picture-side label. A large mean step with aligned row
// structure is an exposure shift; a changed structure is a normal cut; any
// boundary touching a missing page, or staying under both thresholds, is kept
// as inconclusive instead of being forced into a verdict.
func classify(seam *Seam, before, after pageProfile) {
	if !before.stat.Exists || !after.stat.Exists {
		seam.Decision = Inconclusive
		seam.Reason = "相邻页存在缺页，无法比对整页曝光。"
		return
	}
	delta := (after.stat.Mean - before.stat.Mean) / 65535
	structure := rowStructure(before, after)
	seam.LevelDelta = delta
	seam.Structure = structure
	scale := math.Abs(delta)
	switch {
	case scale >= levelShift && structure <= lowStructure && structure < scale/3:
		seam.Decision = ExposureShift
		seam.Reason = "整页平均亮度明显平移，行剖面结构基本保持。"
	case structure > lowStructure:
		seam.Decision = PictureCut
		seam.Reason = "行剖面结构发生变化，更像正常画面切换。"
	default:
		seam.Decision = Inconclusive
		seam.Reason = "亮度与结构变化都不显著，没有足够证据下结论。"
	}
}

// rowStructure fits a least-squares affine relationship between the two pages'
// mean-removed row profiles and returns the residual fraction. A whole-page
// exposure change scales both mean and spread by the same factor, so the fit
// absorbs the gain step and leaves a near-zero residual; a genuine content cut
// cannot be explained by a single gain and leaves a large residual.
func rowStructure(before, after pageProfile) float64 {
	if before.stat.Sigma == 0 {
		return 0
	}
	// All moments use averaged (per-row) scales so the least-squares gain is
	// dimensionless. Summing the covariance while dividing by an averaged
	// variance would inflate the gain by the row count.
	var covariance float64
	for row := range before.rowMean {
		b := before.rowMean[row] - before.stat.Mean
		a := after.rowMean[row] - after.stat.Mean
		covariance += b * a
	}
	covariance /= float64(len(before.rowMean))
	gain := covariance / (before.stat.Sigma * before.stat.Sigma)
	var residual float64
	for row := range before.rowMean {
		b := before.rowMean[row] - before.stat.Mean
		a := after.rowMean[row] - after.stat.Mean
		diff := a - gain*b
		residual += diff * diff
	}
	// Normalize against the after-page spread so the score is a unit-free
	// fraction of the observed variation rather than an absolute intensity.
	return math.Sqrt(residual/float64(len(before.rowMean))) / after.stat.Sigma
}

func pictureLocator(reel *capture.Reel, frame int) Locator {
	row := frame * reel.Rows
	locator := Locator{PictureRow: row, Frame: frame, Row: 0, Status: register.Readable}
	if _, ok := reel.Sheets[frame]; !ok {
		locator.Status = register.Missing
		return locator
	}
	return locator
}

// soundLocator resolves the first picture row of the new page through the lead
// offset, which is the moment the audible lanes cross into their next source
// page. With a positive lead the sound moves first; with a negative lead the
// audible change can lag or land outside the capture envelope.
func soundLocator(reel *capture.Reel, frame int, profiles []pageProfile) SoundSide {
	row := frame * reel.Rows
	source, status := register.Locate(reel, row)
	sound := SoundSide{
		Locator:  Locator{PictureRow: row, Status: status},
		Evidence: SoundMissing,
	}
	if status != register.Outside {
		sound.Locator.Frame = source.Frame
		sound.Locator.Row = source.Row
		sound.Locator.Source = &source
	}
	if status != register.Readable || source.Frame == 0 {
		return sound
	}
	before, after := profiles[source.Frame-1], profiles[source.Frame]
	if !before.stat.Exists || !after.stat.Exists {
		return sound
	}
	sound.LaneBefore = before.laneMean
	sound.LaneAfter = after.laneMean
	strong := false
	for lane := range sound.LevelDelta {
		delta := (after.laneMean[lane] - before.laneMean[lane]) / 65535
		sound.LevelDelta[lane] = delta
		if math.Abs(delta) >= soundShift {
			strong = true
		}
	}
	if strong {
		sound.Evidence = SoundStrong
	} else {
		sound.Evidence = SoundWeak
	}
	return sound
}
