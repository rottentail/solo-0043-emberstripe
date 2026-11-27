package diffreview

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rottentail/solo-0043-emberstripe/internal/wave"
)

// ArtifactFiles locates the written review artifact.
type ArtifactFiles struct {
	Dir        string `json:"dir"`
	ReportJSON string `json:"report_json"`
	Markdown   string `json:"report_md"`
	HTML       string `json:"report_html"`
	MasterWAV  string `json:"review_wav"`
	BundleZip  string `json:"bundle_zip"`
	ZipSHA256  string `json:"bundle_sha256"`
}

// WriteArtifact renders JSON, Chinese Markdown/HTML, listening clips and a
// zipped bundle into dir. The report is updated with relative file names.
func WriteArtifact(rep *Report, dir string) (ArtifactFiles, error) {
	var files ArtifactFiles
	if err := os.MkdirAll(filepath.Join(dir, "clips"), 0o750); err != nil {
		return files, err
	}
	if rep.GeneratedAt == "" {
		rep.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	}

	// 1. Per-region clips and master review WAV.
	master := make([]int16, 0)
	gapMaster := make([]int16, int(0.7*float64(rep.rc))*2)
	for i := range rep.Regions {
		r := &rep.Regions[i]
		base := fmt.Sprintf("region-%02d-%s", i+1, r.Mark)

		// Exact native-rate excerpts from each original parcel over the same
		// padded, neighbor-clamped clip span.
		aNative, err := nativeClip(rep.src[0], r.ClipSpan.FrameStart, r.ClipSpan.FrameEnd)
		if err != nil {
			return files, err
		}
		bNative, err := nativeClip(rep.src[1], r.ClipSpan.FrameStart, r.ClipSpan.FrameEnd)
		if err != nil {
			return files, err
		}
		aName := filepath.ToSlash(filepath.Join("clips", base+"-a.wav"))
		bName := filepath.ToSlash(filepath.Join("clips", base+"-b.wav"))
		if err := os.WriteFile(filepath.Join(dir, aName), aNative, 0o640); err != nil {
			return files, err
		}
		if err := os.WriteFile(filepath.Join(dir, bName), bNative, 0o640); err != nil {
			return files, err
		}
		r.Files.AWAV = aName
		r.Files.BWAV = bName
		rep.Outputs = append(rep.Outputs, OutputFile{Path: aName, Bytes: len(aNative), SHA256: sha256Bytes(aNative)})
		rep.Outputs = append(rep.Outputs, OutputFile{Path: bName, Bytes: len(bNative), SHA256: sha256Bytes(bNative)})

		// Common-rate A-then-B comparison clip.
		fps := rep.src[0].Index.FPS
		aPCM := resampleClip(rep.src[0], r.ClipSpan.FrameStart, r.ClipSpan.FrameEnd, rep.rc, fps)
		bPCM := resampleClip(rep.src[1], r.ClipSpan.FrameStart, r.ClipSpan.FrameEnd, rep.rc, fps)
		gap := make([]int16, int(0.35*float64(rep.rc))*2)
		cmpPCM := append(append(append([]int16{}, aPCM...), gap...), bPCM...)
		cmpWAV := wave.Encode(rep.rc, cmpPCM)
		cName := filepath.ToSlash(filepath.Join("clips", base+"-compare.wav"))
		if err := os.WriteFile(filepath.Join(dir, cName), cmpWAV, 0o640); err != nil {
			return files, err
		}
		r.Files.CompareWAV = cName
		rep.Outputs = append(rep.Outputs, OutputFile{Path: cName, Bytes: len(cmpWAV), SHA256: sha256Bytes(cmpWAV)})

		master = append(master, cmpPCM...)
		master = append(master, gapMaster...)
	}

	masterWAV := wave.Encode(rep.rc, master)
	masterName := "review.wav"
	if err := os.WriteFile(filepath.Join(dir, masterName), masterWAV, 0o640); err != nil {
		return files, err
	}
	files.MasterWAV = filepath.Join(dir, masterName)
	rep.Outputs = append(rep.Outputs, OutputFile{Path: masterName, Bytes: len(masterWAV), SHA256: sha256Bytes(masterWAV)})
	rep.Summary.Clips = 3 * len(rep.Regions)

	// 2. Markdown and HTML reports.
	md := []byte(RenderMarkdown(rep))
	mdName := "diff-review.md"
	if err := os.WriteFile(filepath.Join(dir, mdName), md, 0o640); err != nil {
		return files, err
	}
	files.Markdown = filepath.Join(dir, mdName)
	rep.Outputs = append(rep.Outputs, OutputFile{Path: mdName, Bytes: len(md), SHA256: sha256Bytes(md)})

	htmlDoc, err := RenderHTML(rep, dir)
	if err != nil {
		return files, err
	}
	htmlName := "diff-review.html"
	if err := os.WriteFile(filepath.Join(dir, htmlName), htmlDoc, 0o640); err != nil {
		return files, err
	}
	files.HTML = filepath.Join(dir, htmlName)
	rep.Outputs = append(rep.Outputs, OutputFile{Path: htmlName, Bytes: len(htmlDoc), SHA256: sha256Bytes(htmlDoc)})

	// Stable ordering for the output manifest.
	sort.Slice(rep.Outputs, func(i, j int) bool { return rep.Outputs[i].Path < rep.Outputs[j].Path })

	// 3. JSON report (manifest of record).
	js, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return files, err
	}
	js = append(js, '\n')
	jsonName := "diff-review.json"
	if err := os.WriteFile(filepath.Join(dir, jsonName), js, 0o640); err != nil {
		return files, err
	}
	files.ReportJSON = filepath.Join(dir, jsonName)

	// 4. Zipped bundle containing the report and every relative output.
	zipPath := filepath.Join(dir, "diff-review.zip")
	zipSHA, err := writeBundleZip(zipPath, dir, jsonName, rep)
	if err != nil {
		return files, err
	}
	files.BundleZip = zipPath
	files.ZipSHA256 = zipSHA
	sidecar := fmt.Sprintf("%s  diff-review.zip\n", zipSHA)
	if err := os.WriteFile(zipPath+".sha256", []byte(sidecar), 0o640); err != nil {
		return files, err
	}
	files.Dir = dir
	return files, nil
}

func nativeClip(s *Source, x0, x1 float64) ([]byte, error) {
	i0, i1 := pcmRangeForX(s, x0, x1)
	i0 = clampInt(i0, 0, s.Header.Frames)
	i1 = clampInt(i1, 0, s.Header.Frames)
	if i1 <= i0 {
		i1 = minInt(i0+1, s.Header.Frames)
	}
	return wave.Encode(s.Header.Rate, s.PCM[2*i0:2*i1]), nil
}

// resampleClip renders the continuous frame-time interval [x0,x1) at rc Hz.
func resampleClip(s *Source, x0, x1 float64, rc, fps int) []int16 {
	if fps <= 0 {
		fps = s.Index.FPS
	}
	n := int(math.Round((x1 - x0) * float64(rc) / float64(fps)))
	if n < 0 {
		n = 0
	}
	out := make([]int16, 2*n)
	stepX := float64(fps) / float64(rc)
	for k := 0; k < n; k++ {
		x := x0 + (float64(k)+0.5)*stepX
		l, rr := sampleAtFrameTime(s, x)
		out[2*k] = floatToPCM16(l)
		out[2*k+1] = floatToPCM16(rr)
	}
	return out
}

func floatToPCM16(v float64) int16 {
	if math.IsNaN(v) {
		return 0
	}
	v = math.Round(v)
	if v > 32767 {
		v = 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func writeBundleZip(zipPath, dir, jsonName string, rep *Report) (string, error) {
	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	zw := zip.NewWriter(f)
	add := func(abs, rel string) error {
		b, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: rel, Method: zip.Deflate})
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	}
	names := []string{jsonName, "diff-review.md", "diff-review.html", "review.wav"}
	for _, o := range rep.Outputs {
		if filepath.Dir(o.Path) == "clips" {
			names = append(names, o.Path)
		}
	}
	seen := map[string]bool{}
	for _, rel := range names {
		if seen[rel] {
			continue
		}
		seen[rel] = true
		if err := add(filepath.Join(dir, rel), rel); err != nil {
			_ = zw.Close()
			_ = f.Close()
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(zipPath)
	if err != nil {
		return "", err
	}
	return sha256Bytes(b), nil
}
