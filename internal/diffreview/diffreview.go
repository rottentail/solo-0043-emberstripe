// Package diffreview builds offline difference listening-review artifacts from
// two verified frozen sound parcels.
//
// The editing desk receives two handoff ZIPs (see package parcel) whose windows
// may only partially overlap and whose row grids may differ even when the frame
// rate is identical. Comparison happens on the continuous frame-time axis:
//
//   - coverage partitions give exact, confirmable readability changes;
//   - jointly readable intervals give measured, evidenced acoustic changes;
//   - when grids differ and the acoustic evidence is inconclusive, the position
//     is marked unaligned ("无法对位") but both original-parcel cue positions
//     are preserved for manual re-listening.
//
// The package is offline: it reads only parcel bytes and writes report/WAV/ZIP
// files; it never starts the station or touches the current scan catalog.
package diffreview

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// Schema identifies the JSON report contract produced by this package.
const Schema = "emberstripe-diff-review/v1"

// Verdict marks a same-frame position in the review artifact.
const (
	// MarkContent: both sides are readable and acoustic evidence confirms a change.
	MarkContent = "content_change" // 内容变化
	// MarkReadability: the parcels' own coverage partitions confirm a coverage/readability difference.
	MarkReadability = "readability_change" // 可读性变化（可确认的覆盖变化）
	// MarkUnaligned: grids differ and evidence is insufficient to claim same or changed.
	MarkUnaligned = "unaligned" // 无法对位
	// MarkSame: compared evidence agrees; omitted from extracted regions.
	MarkSame = "same" // 一致
)

// MarkZh maps verdict codes to the desk-facing Chinese labels.
var MarkZh = map[string]string{
	MarkContent:     "内容变化",
	MarkReadability: "可读性变化",
	MarkUnaligned:   "无法对位",
	MarkSame:        "一致",
}

// Label documents a verdict code for report consumers.
type Label struct {
	Code     string   `json:"code"`
	Zh       string   `json:"zh"`
	En       string   `json:"en"`
	Means    string   `json:"means_zh"`
	Synonyms []string `json:"synonyms,omitempty"`
}

func defaultLegend() map[string]Label {
	return map[string]Label{
		MarkContent: {
			Code: MarkContent, Zh: "内容变化", En: "content/acoustic change",
			Means:    "两侧在该画格时间范围内均可读，声学计量确认 PCM 内容不同（有证据的声学变化）。",
			Synonyms: []string{"acoustic_change", "content_changed", "difference"},
		},
		MarkReadability: {
			Code: MarkReadability, Zh: "可读性变化", En: "readability/coverage change",
			Means:    "两个原包的覆盖分区确认该画格时间范围的可读状态不同（可确认的覆盖变化）。",
			Synonyms: []string{"coverage_change", "coverage_changed", "readability_changed"},
		},
		MarkUnaligned: {
			Code: MarkUnaligned, Zh: "无法对位", En: "unaligned / cannot align",
			Means:    "网格不同（每格行数不同）且声学证据不足，不能判定内容异同；保留两个原包的可复听 cue。",
			Synonyms: []string{"unalignable", "cannot_align", "alignment_failed", "inconclusive", "grid_mismatch"},
		},
		MarkSame: {
			Code: MarkSame, Zh: "一致", En: "same / no difference",
			Means:    "可比较位置证据一致，不抽取为听审区域。",
			Synonyms: []string{"same", "identical", "no_change"},
		},
	}
}

// StatusZh translates coverage status codes for the Chinese report.
var StatusZh = map[string]string{
	register.Readable: "可读",
	register.Missing:  "缺页",
	register.Outside:  "卷外",
}

// Options controls evidence thresholds and clip extraction.
type Options struct {
	// ChangeRMS/ChangePeak are normalized (0..1) cross-grid evidence levels
	// that confirm an acoustic content change. Either channel exceeding either
	// threshold is sufficient.
	ChangeRMS  float64 `json:"change_rms_norm"`
	ChangePeak float64 `json:"change_peak_norm"`
	// SameRMS/SamePeak bound the band that counts as confident agreement on a
	// mismatched grid. Values between Same* and Change* are inconclusive.
	SameRMS  float64 `json:"same_rms_norm"`
	SamePeak float64 `json:"same_peak_norm"`
	// MinSpanSeconds is the shortest jointly readable interval that may support
	// a "same" conclusion on a mismatched grid.
	MinSpanSeconds float64 `json:"min_span_seconds"`
	// PadSeconds extends extracted clips around their exact region span.
	PadSeconds float64 `json:"pad_seconds"`
	// MergeGapSeconds bridges equivalent gaps between actionable findings when
	// grouping review regions. Zero keeps only touching findings together.
	MergeGapSeconds float64 `json:"merge_gap_seconds"`
}

// DefaultOptions returns conservative evidence bands suited to 16-bit PCM.
func DefaultOptions() Options {
	return Options{
		ChangeRMS:       0.05,
		ChangePeak:      0.20,
		SameRMS:         0.006,
		SamePeak:        0.03,
		MinSpanSeconds:  0.02,
		PadSeconds:      0.12,
		MergeGapSeconds: 0.0,
	}
}

// ParcelRef is the provenance record for one of the two original parcels.
type ParcelRef struct {
	Role         string           `json:"role"`
	GivenPath    string           `json:"given_path,omitempty"`
	ParcelID     string           `json:"parcel_id"`
	ParcelSHA256 string           `json:"parcel_sha256"`
	Bytes        int              `json:"bytes"`
	Verified     bool             `json:"verified"`
	Capture      string           `json:"capture"`
	SourceSHA256 string           `json:"source_sha256"`
	AudioSHA256  string           `json:"audio_sha256"`
	Window       register.Window  `json:"window"`
	RowsPerFrame int              `json:"rows_per_frame"`
	FPS          int              `json:"fps"`
	LeadRows     int              `json:"lead_rows"`
	SampleRate   int              `json:"sample_rate"`
	Gauge        parcelGauge      `json:"gauge"`
	Wave         wave.Header      `json:"wave"`
	Coverage     []register.Run   `json:"-"`
	Pictures     []parcel.Picture `json:"-"`
}

// parcelGauge mirrors gauge.Gauge without importing the type chain in the
// provenance docs; JSON stays identical to the parcel index.
type parcelGauge struct {
	Profile string    `json:"profile"`
	Lanes   [2]gRange `json:"lanes"`
}
type gRange struct {
	Dark  float64 `json:"dark"`
	Clear float64 `json:"clear"`
}

// Cue points at a re-listening position inside one original parcel's sound.wav.
type Cue struct {
	Role          string  `json:"role"`
	ParcelID      string  `json:"parcel_id"`
	ParcelPath    string  `json:"parcel_path,omitempty"`
	Entry         string  `json:"entry"`
	Capture       string  `json:"capture"`
	FPS           int     `json:"fps"`
	RowsPerFrame  int     `json:"rows_per_frame"`
	Frame         int     `json:"frame"`
	RowInFrame    int     `json:"row_in_frame"`
	PictureRow    int     `json:"picture_row"`
	PCMFrame      int     `json:"pcm_frame"`
	SampleRate    int     `json:"sample_rate_hz"`
	WAVSeconds    float64 `json:"wav_seconds"`
	WAVTimecode   string  `json:"wav_timecode"`
	WAVByteOffset int     `json:"wav_byte_offset"`
}

// CuePair keeps a re-audition cue for both original packages.
type CuePair struct {
	A Cue `json:"a"`
	B Cue `json:"b"`
}

// Span is a continuous interval on the absolute frame-time axis.
type Span struct {
	FrameStart      float64 `json:"frame_start"`
	FrameEnd        float64 `json:"frame_end"`
	FirstFrame      int     `json:"first_frame"`
	LastFrame       int     `json:"last_frame"`
	DurationFrames  float64 `json:"duration_frames"`
	DurationSeconds float64 `json:"duration_seconds,omitempty"`
	StartSeconds    float64 `json:"start_seconds,omitempty"`
}

// CoverageEvidence is a confirmed difference between the parcels' coverage partitions.
type CoverageEvidence struct {
	Kind                string  `json:"kind"`
	Frame               int     `json:"frame"`
	Span                Span    `json:"span"`
	StatusA             string  `json:"status_a"`
	StatusB             string  `json:"status_b"`
	StatusAZh           string  `json:"status_a_zh"`
	StatusBZh           string  `json:"status_b_zh"`
	ReadableA           bool    `json:"readable_a"`
	ReadableB           bool    `json:"readable_b"`
	AudibleStateChanged bool    `json:"audible_state_changed"`
	Confirmation        string  `json:"confirmation"`
	Cues                CuePair `json:"cues"`
}

// AcousticEvidence holds measured comparison values for a jointly readable interval.
type AcousticEvidence struct {
	Kind               string     `json:"kind"`
	Frame              int        `json:"frame"`
	Span               Span       `json:"span"`
	Basis              string     `json:"basis"` // sample_exact | resampled_continuous_time
	ComparisonRate     int        `json:"comparison_rate,omitempty"`
	ComparedFrames     int        `json:"compared_frames"`
	ChangedSamples     [2]int     `json:"changed_samples,omitempty"`
	ChangedFraction    [2]float64 `json:"changed_fraction,omitempty"`
	PeakDelta          [2]float64 `json:"peak_delta"`
	RMSDelta           [2]float64 `json:"rms_delta"`
	PeakDeltaNorm      [2]float64 `json:"peak_delta_norm"`
	RMSDeltaNorm       [2]float64 `json:"rms_delta_norm"`
	RMSA               [2]float64 `json:"rms_a"`
	RMSB               [2]float64 `json:"rms_b"`
	Conclusion         string     `json:"conclusion"` // content_change | same | inconclusive
	EvidenceSufficient bool       `json:"evidence_sufficient"`
	Reason             string     `json:"reason,omitempty"`
	Cues               CuePair    `json:"cues"`
}

// AlignmentNote explains an "unaligned" verdict on a mismatched grid.
type AlignmentNote struct {
	Frame  int     `json:"frame"`
	Span   Span    `json:"span"`
	Reason string  `json:"reason"`
	Detail string  `json:"detail_zh"`
	Cues   CuePair `json:"cues"`
}

// FrameMark is the single headline mark for one same-frame position.
type FrameMark struct {
	Frame           int                `json:"frame"`
	Mark            string             `json:"mark"`
	Verdict         string             `json:"verdict"`
	MarkZh          string             `json:"mark_zh"`
	GridAligned     bool               `json:"grid_aligned"`
	RegionID        string             `json:"region_id,omitempty"`
	CoverageChanges []CoverageEvidence `json:"coverage_changes,omitempty"`
	Acoustic        []AcousticEvidence `json:"acoustic,omitempty"`
	Unaligned       []AlignmentNote    `json:"unaligned,omitempty"`
	Cues            CuePair            `json:"cues"`
}

// RegionFiles lists extracted listening clips for a review region.
type RegionFiles struct {
	CompareWAV string `json:"compare_wav,omitempty"`
	AWAV       string `json:"a_wav,omitempty"`
	BWAV       string `json:"b_wav,omitempty"`
}

// Region is a clustered, extracted listening-review area.
type Region struct {
	ID               string             `json:"id"`
	Mark             string             `json:"mark"`
	Verdict          string             `json:"verdict"`
	MarkZh           string             `json:"mark_zh"`
	AlsoMarks        []string           `json:"also_marks,omitempty"`
	Frames           []int              `json:"frames"`
	Span             Span               `json:"span"`
	ClipSpan         Span               `json:"clip_span"`
	GridAligned      bool               `json:"grid_aligned"`
	CoverageEvidence []CoverageEvidence `json:"coverage_evidence,omitempty"`
	AcousticEvidence []AcousticEvidence `json:"acoustic_evidence,omitempty"`
	Unaligned        []AlignmentNote    `json:"unaligned,omitempty"`
	Cues             CuePair            `json:"cues"`
	Files            RegionFiles        `json:"files,omitempty"`
	ListeningHintZh  string             `json:"listening_hint_zh"`
}

// ExclusiveSpan documents window portions present in only one original parcel.
type ExclusiveSpan struct {
	Side       string `json:"side"`
	FrameStart int    `json:"frame_start"`
	FrameEnd   int    `json:"frame_end"`
	Frames     int    `json:"frames"`
	NoteZh     string `json:"note_zh"`
	Cue        Cue    `json:"cue"`
}

// Timebase records the alignment basis for the two parcels.
type Timebase struct {
	FPSA           int    `json:"fps_a"`
	FPSB           int    `json:"fps_b"`
	SameFPS        bool   `json:"same_fps"`
	RowsA          int    `json:"rows_per_frame_a"`
	RowsB          int    `json:"rows_per_frame_b"`
	SameGrid       bool   `json:"same_grid"`
	ComparisonRate int    `json:"comparison_rate_hz"`
	AlignmentBasis string `json:"alignment_basis"`
}

// Summary counts marks and outputs.
type Summary struct {
	OverlapFrames      int            `json:"overlap_frames"`
	FramesByMark       map[string]int `json:"frames_by_mark"`
	Regions            int            `json:"regions"`
	RegionsByMark      map[string]int `json:"regions_by_mark"`
	CoverageChanges    int            `json:"coverage_change_intervals"`
	AcousticChanges    int            `json:"acoustic_change_intervals"`
	UnalignedIntervals int            `json:"unaligned_intervals"`
	SameIntervals      int            `json:"same_intervals"`
	ExclusiveFramesA   int            `json:"exclusive_frames_a"`
	ExclusiveFramesB   int            `json:"exclusive_frames_b"`
	Clips              int            `json:"clip_files"`
}

// Policy documents the decision rules embedded in the report.
type Policy struct {
	Options
	FrameMarkPrecedence string `json:"frame_mark_precedence"`
	NoteZh              string `json:"note_zh"`
}

// OutputFile describes one artifact file.
type OutputFile struct {
	Path   string `json:"path"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Report is the complete offline difference listening-review artifact index.
type Report struct {
	Schema      string           `json:"schema"`
	Kind        string           `json:"kind"`
	TitleZh     string           `json:"title_zh"`
	Legend      map[string]Label `json:"legend"`
	GeneratedAt string           `json:"generated_at,omitempty"`
	Sources     [2]ParcelRef     `json:"sources"`
	Timebase    Timebase         `json:"timebase"`
	Overlap     *register.Window `json:"overlap,omitempty"`
	OverlapSpan *Span            `json:"overlap_span,omitempty"`
	Frames      []FrameMark      `json:"frames"`
	Regions     []Region         `json:"regions"`
	Exclusive   []ExclusiveSpan  `json:"exclusive,omitempty"`
	Summary     Summary          `json:"summary"`
	Policy      Policy           `json:"policy"`
	Outputs     []OutputFile     `json:"outputs,omitempty"`
	BundleFile  string           `json:"bundle_file,omitempty"`
	Notes       []string         `json:"notes,omitempty"`

	// loaded sources retained for clip rendering; not serialized.
	src [2]*Source `json:"-"`
	opt Options    `json:"-"`
	rc  int        `json:"-"`
}

// Source is a verified parcel decoded for comparison.
type Source struct {
	Ref    ParcelRef
	Index  parcel.Index
	Header wave.Header
	PCM    []int16 // interleaved stereo, length 2*Header.Frames
	Bytes  []byte
}

// LoadParcel reads and verifies a frozen parcel ZIP. Verification failure is
// returned as an error so callers never compare an untrustworthy handoff.
func LoadParcel(path string) (*Source, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read parcel %q: %w", path, err)
	}
	receipt, err := parcel.Verify(data)
	if err != nil {
		return nil, fmt.Errorf("parcel %q failed verification: %w", path, err)
	}
	return newSource(path, data, receipt)
}

// LoadParcelBytes is the byte-slice variant used by store IDs and tests.
func LoadParcelBytes(pathHint string, data []byte) (*Source, error) {
	receipt, err := parcel.Verify(data)
	if err != nil {
		return nil, fmt.Errorf("parcel %q failed verification: %w", pathHint, err)
	}
	return newSource(pathHint, data, receipt)
}

func newSource(path string, data []byte, receipt parcel.Receipt) (*Source, error) {
	idx := receipt.Index
	header := receipt.Wave
	wav, err := parcelWAVBytes(data)
	if err != nil {
		return nil, err
	}
	if len(wav) < 44+4*header.Frames {
		return nil, errors.New("parcel sound.wav truncated")
	}
	pcm := make([]int16, 2*header.Frames)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(wav[44+2*i:]))
	}
	ref := ParcelRef{
		GivenPath:    path,
		ParcelID:     receipt.ID,
		ParcelSHA256: receipt.ID,
		Bytes:        receipt.Bytes,
		Verified:     true,
		Capture:      idx.Capture,
		SourceSHA256: idx.Source,
		AudioSHA256:  idx.Audio,
		Window:       idx.Window,
		RowsPerFrame: idx.Rows,
		FPS:          idx.FPS,
		LeadRows:     idx.Lead,
		SampleRate:   header.Rate,
		Gauge: parcelGauge{Profile: idx.Gauge.Profile, Lanes: [2]gRange{
			{Dark: idx.Gauge.Lanes[0].Dark, Clear: idx.Gauge.Lanes[0].Clear},
			{Dark: idx.Gauge.Lanes[1].Dark, Clear: idx.Gauge.Lanes[1].Clear},
		}},
		Wave:     header,
		Coverage: idx.Coverage,
		Pictures: idx.Pictures,
	}
	return &Source{Ref: ref, Index: idx, Header: header, PCM: pcm, Bytes: data}, nil
}

// parcelWAVBytes extracts sound.wav from a verified parcel ZIP.
func parcelWAVBytes(data []byte) ([]byte, error) {
	entries, err := zipEntries(data)
	if err != nil {
		return nil, err
	}
	wav, ok := entries["sound.wav"]
	if !ok {
		return nil, errors.New("sound.wav missing from verified parcel")
	}
	return wav, nil
}

// ResolveParcelRef turns a -a/-b argument into parcel bytes: a path to a ZIP,
// or a 64-hex parcel id resolved under storeDir as <id>.zip.
func ResolveParcelRef(value, storeDir string) (path string, data []byte, err error) {
	if value == "" {
		return "", nil, errors.New("parcel reference is required")
	}
	if info, statErr := os.Stat(value); statErr == nil && !info.IsDir() {
		b, readErr := os.ReadFile(value)
		return value, b, readErr
	}
	if isDigestID(value) && storeDir != "" {
		p := storeDir + string(os.PathSeparator) + value + ".zip"
		b, readErr := os.ReadFile(p)
		return p, b, readErr
	}
	if isDigestID(value) {
		return "", nil, fmt.Errorf("%q looks like a parcel id but no -parcels store was given", value)
	}
	return "", nil, fmt.Errorf("parcel not found: %q", value)
}

func isDigestID(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Compare analyses two verified sources and returns the report without writing.
func Compare(a, b *Source, opt Options) (*Report, error) {
	if a == nil || b == nil {
		return nil, errors.New("two parcels are required")
	}
	opt = withDefaults(opt)
	ra, rb := a.Index.Rows, b.Index.Rows
	fa, fb := a.Index.FPS, b.Index.FPS
	sameFPS := fa == fb
	sameGrid := sameFPS && ra == rb

	rc := a.Header.Rate
	if b.Header.Rate > rc {
		rc = b.Header.Rate
	}
	if !sameGrid {
		// Continuous-time comparison runs on the finer native grid rate.
		if sameFPS {
			rc = fa * maxInt(ra, rb)
		}
	}
	if rc < 8000 || rc > 96000 {
		rc = 48000
	}

	rep := &Report{
		Schema:  Schema,
		Kind:    "offline_parcel_diff_review",
		TitleZh: "离线差异听审报告（两个冻结交接包对照）",
		Legend:  defaultLegend(),
		Sources: [2]ParcelRef{withRole(a.Ref, "a"), withRole(b.Ref, "b")},
		Timebase: Timebase{
			FPSA: fa, FPSB: fb, SameFPS: sameFPS,
			RowsA: ra, RowsB: rb, SameGrid: sameGrid,
			ComparisonRate: rc,
		},
		Policy: Policy{
			Options:             opt,
			FrameMarkPrecedence: MarkContent + " > " + MarkReadability + " > " + MarkUnaligned + " > " + MarkSame,
			NoteZh:              "覆盖状态差异只由两包 register.json 的覆盖分区确认；补零缺口不充当声学证据。同网格逐样本比较；异网格按画格时间连续重采样后计量，证据落在一致与变化阈值之间时判为无法对位并保留双包复听位置。",
		},
		src: [2]*Source{a, b},
		opt: opt,
		rc:  rc,
	}
	switch {
	case sameGrid:
		rep.Timebase.AlignmentBasis = "sample_exact"
	case sameFPS:
		rep.Timebase.AlignmentBasis = "continuous_time_linear_resample"
	default:
		rep.Timebase.AlignmentBasis = "frame_rate_mismatch_time_resample"
		rep.Notes = append(rep.Notes, "两包画格率不同，画格编号不共享同一时间基；以下按秒连续对照，画格标记仅作各自编号提示，建议人工复听确认。")
	}

	wa, wb := a.Index.Window, b.Index.Window
	l := maxInt(wa.Start, wb.Start)
	u := minInt(wa.End, wb.End)

	rep.Exclusive = buildExclusive(a, b, l, u)
	rep.Summary.FramesByMark = map[string]int{}
	rep.Summary.RegionsByMark = map[string]int{}

	if l >= u {
		rep.Notes = append(rep.Notes, "两个发布窗没有共同画格，无法在同一画格位置对照；仅登记各自独占区间与复听位置。")
		rep.Summary.ExclusiveFramesA = wa.End - wa.Start
		rep.Summary.ExclusiveFramesB = wb.End - wb.Start
		return rep, nil
	}

	rep.Overlap = &register.Window{Start: l, End: u}
	commonFPS := fa
	if !sameFPS {
		commonFPS = fa // frame-coordinate spans still reported; seconds use A's base where ambiguous
	}
	span := makeSpan(float64(l), float64(u), commonFPS)
	rep.OverlapSpan = &span

	findings := segmentAndMeasure(rep, l, u)
	buildFrameMarks(rep, findings, l, u)
	buildRegions(rep, findings)
	fillSummary(rep, findings, l, u)
	return rep, nil
}

func withDefaults(o Options) Options {
	d := DefaultOptions()
	if o.ChangeRMS == 0 && o.ChangePeak == 0 && o.SameRMS == 0 && o.SamePeak == 0 {
		return d
	}
	if o.ChangeRMS <= 0 {
		o.ChangeRMS = d.ChangeRMS
	}
	if o.ChangePeak <= 0 {
		o.ChangePeak = d.ChangePeak
	}
	if o.SameRMS <= 0 {
		o.SameRMS = d.SameRMS
	}
	if o.SamePeak <= 0 {
		o.SamePeak = d.SamePeak
	}
	if o.MinSpanSeconds <= 0 {
		o.MinSpanSeconds = d.MinSpanSeconds
	}
	if o.PadSeconds <= 0 {
		o.PadSeconds = d.PadSeconds
	}
	return o
}

func withRole(r ParcelRef, role string) ParcelRef { r.Role = role; return r }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func makeSpan(x0, x1 float64, fps int) Span {
	s := Span{
		FrameStart:     x0,
		FrameEnd:       x1,
		FirstFrame:     int(math.Floor(x0 + 1e-9)),
		LastFrame:      int(math.Ceil(x1-1e-9)) - 1,
		DurationFrames: x1 - x0,
	}
	if fps > 0 {
		s.StartSeconds = x0 / float64(fps)
		s.DurationSeconds = (x1 - x0) / float64(fps)
	}
	if x1 <= x0 {
		s.LastFrame = s.FirstFrame
	}
	return s
}

// cueAt builds the re-audition cue for source src at continuous frame coord x.
func cueAt(src *Source, x float64, role string) Cue {
	idx := src.Index
	r := idx.Rows
	pictureRow := int(math.Round(x * float64(r)))
	pictureRow = clampInt(pictureRow, idx.Window.Start*r, idx.Window.End*r-1)
	pcmFrame := pictureRow - idx.Window.Start*r
	pcmFrame = clampInt(pcmFrame, 0, src.Header.Frames-1)
	pictureRow = pcmFrame + idx.Window.Start*r
	frame := pictureRow / r
	row := pictureRow % r
	secs := float64(pcmFrame) / float64(src.Header.Rate)
	tc := timecode(secs)
	path := src.Ref.GivenPath
	return Cue{
		Role: role, ParcelID: src.Ref.ParcelID, ParcelPath: path, Entry: "sound.wav",
		Capture: idx.Capture, FPS: idx.FPS, RowsPerFrame: r,
		Frame: frame, RowInFrame: row, PictureRow: pictureRow,
		PCMFrame: pcmFrame, SampleRate: src.Header.Rate,
		WAVSeconds: secs, WAVTimecode: tc,
		WAVByteOffset: 44 + 4*pcmFrame,
	}
}

// CuePairAt returns cues for both parcels at the same frame coordinate.
func CuePairAt(rep *Report, x float64) CuePair {
	return CuePair{A: cueAt(rep.src[0], x, "a"), B: cueAt(rep.src[1], x, "b")}
}

func clampInt(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func timecode(sec float64) string {
	if sec < 0 {
		sec = 0
	}
	ms := int(math.Round(sec * 1000))
	h := ms / 3600000
	ms -= h * 3600000
	m := ms / 60000
	ms -= m * 60000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func sha256Bytes(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// SHA256Hex returns the lowercase hex SHA-256 of b.
func SHA256Hex(b []byte) string { return sha256Bytes(b) }

// IsDigestID reports whether s is a 64-char lowercase hex parcel id.
func IsDigestID(s string) bool { return isDigestID(s) }
