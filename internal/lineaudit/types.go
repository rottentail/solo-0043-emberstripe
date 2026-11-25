// Package lineaudit compares two scanner captures on their raw line clock.
//
// It is deliberately read-only: callers receive conclusions and URLs, but no
// scan bytes or existing parcels are modified.
package lineaudit

// Request selects two compatible trial scans and an optional source-row window.
type Request struct {
	A        string `json:"a"`
	B        string `json:"b"`
	StartRow int    `json:"start_row"`
	EndRow   int    `json:"end_row"`
	// Profile and ProfileB select gauge profiles. An empty profile means the
	// reel's first profile; callers that need the workstation's usual default
	// can resolve it before calling Compare.
	Profile  string `json:"profile"`
	ProfileB string `json:"profile_b"`
	MaxDrift int    `json:"max_drift_rows"`
}

// Interval is a half-open interval on a reel's raw scanner-row clock.
type Interval struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Address identifies one absolute scanner row and the emulsion page containing it.
type Address struct {
	Row               int    `json:"row"`
	Valid             bool   `json:"valid"`
	Frame             int    `json:"frame"`
	RowInFrame        int    `json:"row_in_frame"`
	SheetAvailable    bool   `json:"sheet_available"`
	PictureRow        int    `json:"picture_row"`
	PictureValid      bool   `json:"picture_valid"`
	PictureFrame      int    `json:"picture_frame,omitempty"`
	PictureRowInFrame int    `json:"picture_row_in_frame,omitempty"`
	WorkbenchURL      string `json:"workbench_url,omitempty"`
	StripURL          string `json:"strip_url,omitempty"`
	AuditionURL       string `json:"audition_url,omitempty"`
}

// Side describes one compared capture.
type Side struct {
	Name      string `json:"name"`
	Rows      int    `json:"rows_per_frame"`
	Frames    int    `json:"frames"`
	TotalRows int    `json:"total_rows"`
	FPS       int    `json:"fps"`
	LeadRows  int    `json:"lead_rows"`
	Digest    string `json:"sha256"`
}

// Evidence is one sound-lane's contribution to a paired-line conclusion.
type Evidence struct {
	Lane            int      `json:"lane"`
	Status          string   `json:"status"`
	Note            string   `json:"note,omitempty"`
	DeltaRows       int      `json:"delta_rows,omitempty"`
	BeforeShiftRows int      `json:"before_shift_rows,omitempty"`
	AfterShiftRows  int      `json:"after_shift_rows,omitempty"`
	AInterval       Interval `json:"a_interval"`
	BInterval       Interval `json:"b_interval"`
	PlausibleA      Interval `json:"plausible_a_interval,omitempty"`
	Score           float64  `json:"score"`
	Matches         int      `json:"matches"`
	PrecisionRows   int      `json:"precision_rows,omitempty"`
}

// LinkSet contains re-audition and original-page entry points for one side.
type LinkSet struct {
	Reel         string `json:"reel"`
	AuditionURL  string `json:"audition_url,omitempty"`
	WorkbenchURL string `json:"workbench_url,omitempty"`
	StripURL     string `json:"strip_url,omitempty"`
}

// Finding is one operator-facing conclusion.
type Finding struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Type           string     `json:"type,omitempty"`
	Verdict        string     `json:"verdict,omitempty"`
	Classification string     `json:"classification"`
	Label          string     `json:"label"`
	Severity       string     `json:"severity"`
	Summary        string     `json:"summary"`
	Confidence     float64    `json:"confidence"`
	DeltaRows      int        `json:"delta_rows,omitempty"`
	StartRow       int        `json:"start_row"`
	EndRow         int        `json:"end_row"`
	AInterval      Interval   `json:"a_interval"`
	BInterval      Interval   `json:"b_interval"`
	RangeA         Interval   `json:"range_a"`
	RangeB         Interval   `json:"range_b"`
	FocusA         *Interval  `json:"focus_a_interval,omitempty"`
	AddressA       *Address   `json:"address_a,omitempty"`
	AddressB       *Address   `json:"address_b,omitempty"`
	LinksA         LinkSet    `json:"links_a,omitempty"`
	LinksB         LinkSet    `json:"links_b,omitempty"`
	Evidence       []Evidence `json:"evidence,omitempty"`
}

// ShiftPoint is a downsampled displacement observation for visualization.
type ShiftPoint struct {
	ARow  int `json:"a_row"`
	Shift int `json:"shift_b_minus_a"`
}

// Report is the read-only result of pairing two trial scans.
type Report struct {
	Schema          string       `json:"schema"`
	A               Side         `json:"a"`
	B               Side         `json:"b"`
	Request         Request      `json:"request"`
	Truncated       bool         `json:"truncated_to_workstation_limit"`
	GlobalShiftRows int          `json:"global_shift_rows"`
	LeadDifference  int          `json:"manifest_lead_difference_rows"`
	Findings        []Finding    `json:"findings"`
	Suspects        []Finding    `json:"suspects"`
	Insertions      []Finding    `json:"insertions"`
	Deletions       []Finding    `json:"deletions"`
	MissingPages    []Finding    `json:"missing_pages"`
	NormalRepeats   []Finding    `json:"normal_repeats"`
	ShiftCurve      []ShiftPoint `json:"shift_curve,omitempty"`
}

const (
	KindInsertion    = "suspect_insertion"
	KindDeletion     = "suspect_deletion"
	KindHeadOffset   = "head_offset"
	KindMissingSheet = "missing_sheet"
	KindNormalRepeat = "normal_repeated_voiceprint"
	KindAligned      = "aligned"

	EvidencePrecise   = "precise"
	EvidenceRangeOnly = "range_only"
	EvidenceNoSignal  = "no_evidence"
	EvidenceMissing   = "missing_page"
	EvidenceConflict  = "conflicting"
)
