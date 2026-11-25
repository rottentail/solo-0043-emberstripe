package lightbench

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/rottentail/solo-0043-emberstripe/internal/auditcard"
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/parcel"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
	"github.com/rottentail/solo-0043-emberstripe/internal/station"
)

// cardEvidence adapts the live catalog and the frozen parcel store to the
// audit card's provenance interface.
type cardEvidence struct {
	reel            *capture.Reel
	reelPresent     bool
	verifiedID      string
	verifiedCapture string
	verifiedSource  string
	verifiedAudio   string
}

func (e cardEvidence) ReelPresent() bool          { return e.reelPresent }
func (e cardEvidence) ReelName() string           { return e.reel.Name }
func (e cardEvidence) ReelDigest() string         { return e.reel.Digest }
func (e cardEvidence) ParcelVerified() bool       { return e.verifiedID != "" }
func (e cardEvidence) ParcelCapture() string      { return e.verifiedCapture }
func (e cardEvidence) ParcelSourceDigest() string { return e.verifiedSource }
func (e cardEvidence) ParcelAudioDigest() string  { return e.verifiedAudio }

type createCardRequest struct {
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Profile string `json:"profile"`
	Frame   *int   `json:"frame"`
	Row     *int   `json:"row"`
	Note    string `json:"note"`
}

type openCardResponse struct {
	Status          string         `json:"status"`
	Reason          string         `json:"reason"`
	Card            auditcard.Card `json:"card"`
	ReelPresent     bool           `json:"reel_present"`
	ReelDigestMatch bool           `json:"reel_digest_match"`
	ParcelVerified  bool           `json:"parcel_verified"`
	ParcelMatch     bool           `json:"parcel_match"`
	SoundURL        string         `json:"sound_url,omitempty"`
	ContinueURL     string         `json:"continue_url,omitempty"`
}

func decodeCardBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(r.Body, auditcard.MaxCardBytes+1))
	if err != nil {
		problem(w, http.StatusBadRequest, "暂时无法读取审听卡。")
		return nil, false
	}
	if len(data) > auditcard.MaxCardBytes {
		problem(w, http.StatusBadRequest, "审听卡超出大小上限。")
		return nil, false
	}
	return data, true
}

// createCard freezes the applied window into a parcel and writes a portable
// card whose provenance digests come from the registered snapshot, never from
// browser-supplied values.
func (b *bench) createCard(w http.ResponseWriter, r *http.Request) {
	reel, ok := b.reel(w, r)
	if !ok {
		return
	}
	body, ok := decodeCardBody(w, r)
	if !ok {
		return
	}
	var req createCardRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		problem(w, http.StatusBadRequest, "审听卡请求字段无法识别或不是合法 JSON。")
		return
	}
	window := register.Window{Start: req.Start, End: req.End}
	if register.Validate(reel.Header, window) != nil {
		problem(w, http.StatusBadRequest, "请填写卷内的起止画格，每窗最多 48 格。")
		return
	}
	frame, row := window.Start, 0
	if req.Frame != nil {
		frame = *req.Frame
	}
	if req.Row != nil {
		row = *req.Row
	}
	if frame < window.Start || frame >= window.End || row < 0 || row >= reel.Rows {
		problem(w, http.StatusBadRequest, "正在听的画格行必须落在已应用窗口内。")
		return
	}
	if _, _, err := station.RenderWindow(reel, window, req.Profile); err != nil {
		problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
		return
	}
	receipt, err := b.station.Release(reel, window, req.Profile)
	if err != nil {
		var input *station.InvalidRenderingInput
		if errors.As(err, &input) {
			problem(w, http.StatusUnprocessableEntity, "该标定缺少参考页或灰阶对比不足。")
			return
		}
		problem(w, http.StatusInternalServerError, "冻结交接包失败，暂时无法生成审听卡。")
		return
	}
	card, err := auditcard.New(reel, window, frame, row, receipt, req.Note)
	if err != nil {
		problem(w, http.StatusBadRequest, "审听卡内容不完整："+err.Error()+"。")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"card": card, "parcel_id": receipt.ID})
}

// openCard assesses an uploaded card against the currently registered catalog
// and the parcel store, then reports which evidence level is supportable.
func (b *bench) openCard(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeCardBody(w, r)
	if !ok {
		return
	}
	card, err := auditcard.Parse(body)
	if err != nil {
		problem(w, http.StatusBadRequest, "这不是可识别的审听卡，或卡内字段不完整。")
		return
	}
	evidence := b.evidenceFor(card)
	assessment := auditcard.Assess(card, evidence)
	resp := openCardResponse{
		Status: assessment.Status, Reason: assessment.Reason, Card: assessment.Card,
		ReelPresent: assessment.ReelPresent, ReelDigestMatch: assessment.ReelDigestMatch,
		ParcelVerified: assessment.ParcelVerified, ParcelMatch: assessment.ParcelMatch,
	}
	if assessment.ParcelMatch {
		resp.SoundURL = "/parcels/" + card.ParcelID + "/sound"
	}
	if assessment.ReelDigestMatch {
		query := "start=" + strconv.Itoa(card.Window.Start) + "&end=" + strconv.Itoa(card.Window.End) +
			"&profile=" + card.Profile + "&frame=" + strconv.Itoa(card.Cursor.Frame) +
			"&row=" + strconv.Itoa(card.Cursor.Row)
		resp.ContinueURL = "/lightbench/" + card.Reel + "?" + query
	}
	writeJSON(w, http.StatusOK, resp)
}

func (b *bench) evidenceFor(card auditcard.Card) cardEvidence {
	evidence := cardEvidence{}
	if reel, ok := b.station.Reel(card.Reel); ok {
		evidence.reel, evidence.reelPresent = reel, true
	}
	if data, err := b.station.Store.Read(card.ParcelID); err == nil {
		if receipt, err := parcel.Verify(data); err == nil && receipt.ID == card.ParcelID {
			evidence.verifiedID = receipt.ID
			evidence.verifiedCapture = receipt.Index.Capture
			evidence.verifiedSource = receipt.Index.Source
			evidence.verifiedAudio = receipt.Index.Audio
		}
	}
	return evidence
}
