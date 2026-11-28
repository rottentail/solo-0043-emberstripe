// Command handoff-demo publishes parcels from the two synthetic reels and
// walks the handoff area to show why the same reported point can be audible
// from different in-parcel positions, only padded with zeros, or unverifiable.
//
// It talks to the running station over its production HTTP surface, so every
// verdict shown comes from the same frozen-register and integrity logic an
// operator uses.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
)

type finding struct {
	ParcelID        string         `json:"parcel_id"`
	Capture         string         `json:"capture"`
	Mode            string         `json:"address_mode"`
	Verdict         string         `json:"verdict"`
	RowsPerFrame    int            `json:"rows_per_frame"`
	LeadRows        int            `json:"lead_rows"`
	Rate            int            `json:"sample_rate"`
	QueryFrame      int            `json:"query_frame"`
	QueryRow        int            `json:"query_row"`
	PictureRow      int            `json:"picture_row"`
	Source          *sourceAddress `json:"source"`
	Window          window         `json:"window"`
	InsideWindow    bool           `json:"inside_window"`
	SampleFrame     *int           `json:"sample_frame"`
	CoverageStatus  string         `json:"coverage_status"`
	PictureInParcel bool           `json:"picture_in_parcel"`
	HasPictureSheet bool           `json:"has_picture_sheet"`
	NonZeroPCM      *bool          `json:"pcm_nonzero"`
}

type sourceAddress struct {
	Frame int `json:"frame"`
	Row   int `json:"row"`
}

type window struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type page struct {
	Token     string `json:"snapshot_token"`
	NextToken string `json:"next_token"`
	Exhausted bool   `json:"exhausted"`
	Items     []struct {
		Finding finding `json:"finding"`
	} `json:"items"`
	Totals map[string]int `json:"totals"`
}

func main() {
	base := flag.String("base", "http://127.0.0.1:7431", "station base URL")
	flag.Parse()
	client := &demoClient{base: strings.TrimRight(*base, "/"), http: http.DefaultClient}
	if err := client.run(); err != nil {
		fmt.Fprintf(os.Stderr, "demo failed: %v\n", err)
		os.Exit(1)
	}
}

type demoClient struct {
	base string
	http *http.Client
}

func (d *demoClient) run() error {
	fmt.Println("== 第一步：在两种偏移的卷上各发布冻结交接包 ==")
	cinder, err := d.release("cinder-a", 1, 4, "soft")
	if err != nil {
		return err
	}
	vireo, err := d.release("vireo-b", 0, 3, "bench")
	if err != nil {
		return err
	}
	fmt.Printf("cinder-a（每格 %d 行，偏移 %+d）包 %s\n", cinder.index.Rows, cinder.index.Lead, short(cinder.id))
	fmt.Printf("vireo-b （每格 %d 行，偏移 %+d）包 %s\n", vireo.index.Rows, vireo.index.Lead, short(vireo.id))

	fmt.Println("\n== 第二步：用同一个扫描室报点（声槽页内行）查找 ==")
	// The scanner reports "sound page 2 row 0". Each parcel converts this
	// with its own lead offset and rows-per-frame clock.
	report := url.Values{"mode": {"source"}, "frame": {"2"}, "row": {"0"}}
	findings, err := d.searchAll(report)
	if err != nil {
		return err
	}
	for _, f := range findings {
		printFinding(f)
	}

	fmt.Println("\n== 第三步：用画格行报点核对同一物理位置 ==")
	// Picture frame 1 row 1770 on cinder-a is the documented missing-sheet
	// boundary: it keeps a clock slot but only carries padding zeros.
	pictureReport := url.Values{"mode": {"picture"}, "frame": {"1"}, "row": {"1770"}, "capture": {"cinder-a"}}
	pictureFindings, err := d.searchAll(pictureReport)
	if err != nil {
		return err
	}
	for _, f := range pictureFindings {
		printFinding(f)
	}

	fmt.Println("\n== 第四步：点开单个包，按当前字节重新复核并读取 PCM 帧 ==")
	for _, f := range pictureFindings {
		detail, err := d.locate(f.ParcelID, pictureReport)
		if err != nil {
			return err
		}
		nonZero := "未知"
		if detail.NonZeroPCM != nil {
			if *detail.NonZeroPCM {
				nonZero = "非零（确有声音）"
			} else {
				nonZero = "为零（补零占位）"
			}
		}
		fmt.Printf("包 %s 复核结论=%s，采样帧 %v 的 PCM %s\n",
			short(f.ParcelID), detail.Verdict, frameLabel(detail.SampleFrame), nonZero)
	}

	fmt.Println("\n== 第五步：一个包窗外的报点必须无法核实 ==")
	outside := url.Values{"mode": {"picture"}, "frame": {"6"}, "row": {"0"}, "capture": {"cinder-a"}}
	outFindings, err := d.searchAll(outside)
	if err != nil {
		return err
	}
	for _, f := range outFindings {
		printFinding(f)
	}

	fmt.Println("\n演示完成。结论：同一报点在不同包时钟下折算到不同画格行与采样帧；")
	fmt.Println("只有该包自己的覆盖声明可读时才“确有声音”，窗内补零只是“时间范围”，窗外则“无法核实”。")
	return nil
}

type released struct {
	id    string
	index struct {
		Rows int `json:"rows_per_frame"`
		Lead int `json:"lead_rows"`
	}
}

func (d *demoClient) release(reel string, start, end int, profile string) (released, error) {
	q := url.Values{"start": {fmt.Sprint(start)}, "end": {fmt.Sprint(end)}, "profile": {profile}}
	var r released
	body, err := d.post(fmt.Sprintf("/captures/%s/release?%s", reel, q.Encode()))
	if err != nil {
		return r, err
	}
	var envelope struct {
		ID    string `json:"id"`
		Index struct {
			Rows int `json:"rows_per_frame"`
			Lead int `json:"lead_rows"`
		} `json:"index"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return r, err
	}
	r.id, r.index = envelope.ID, envelope.Index
	return r, nil
}

func (d *demoClient) searchAll(values url.Values) ([]finding, error) {
	var out []finding
	token := ""
	for {
		q := values
		if token != "" {
			q.Set("snapshot", token)
		}
		body, err := d.get("/handoff/parcels?" + q.Encode())
		if err != nil {
			return nil, err
		}
		var p page
		if err := json.Unmarshal(body, &p); err != nil {
			return nil, err
		}
		for _, item := range p.Items {
			out = append(out, item.Finding)
		}
		if p.Exhausted {
			break
		}
		token = p.NextToken
		if token == "" {
			return nil, fmt.Errorf("server offered another page without a snapshot token")
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ParcelID < out[j].ParcelID })
	return out, nil
}

func (d *demoClient) locate(id string, values url.Values) (finding, error) {
	body, err := d.get(fmt.Sprintf("/handoff/parcels/%s/locate?%s", id, values.Encode()))
	if err != nil {
		return finding{}, err
	}
	var envelope struct {
		Finding finding `json:"finding"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return finding{}, err
	}
	return envelope.Finding, nil
}

func (d *demoClient) get(path string) ([]byte, error)  { return d.do(http.MethodGet, path) }
func (d *demoClient) post(path string) ([]byte, error) { return d.do(http.MethodPost, path) }

func (d *demoClient) do(method, path string) ([]byte, error) {
	req, err := http.NewRequest(method, d.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s -> %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func printFinding(f finding) {
	src := "无"
	if f.Source != nil {
		src = fmt.Sprintf("%d 页 %d 行", f.Source.Frame, f.Source.Row)
	}
	verdict := map[string]string{
		"audible":      "确有声音",
		"time_range":   "只有时间范围（补零）",
		"unverifiable": "没有足够材料核实",
	}[f.Verdict]
	fmt.Printf("• 包 %s [%s] 时钟=%d行/格 偏移%+d\n", short(f.ParcelID), f.Capture, f.RowsPerFrame, f.LeadRows)
	fmt.Printf("    报点(%s) %d页/格 %d行 → 画格行 %d → 源声槽 %s；窗 %d–%d 格，采样帧 %s，覆盖=%s，结论：%s\n",
		f.Mode, f.QueryFrame, f.QueryRow, f.PictureRow, src, f.Window.Start, f.Window.End-1,
		frameLabel(f.SampleFrame), f.CoverageStatus, verdict)
}

func frameLabel(s *int) string {
	if s == nil {
		return "无"
	}
	return fmt.Sprintf("%d", *s)
}

func short(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12] + "…"
}
