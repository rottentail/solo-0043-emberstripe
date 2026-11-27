// Command diffreview builds an offline difference listening-review artifact
// from two verified frozen parcel ZIPs.
//
// Usage:
//
//	diffreview -a handoff-a.zip -b handoff-b.zip -out var/diff-review
//	diffreview -a-id <sha256> -b-id <sha256> -parcels var/parcels -out ...
//	diffreview demo -out var/diff-demo
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rottentail/solo-0043-emberstripe/internal/diffreview"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "demo" {
		runDemo(os.Args[2:])
		return
	}
	runCompare(os.Args[1:])
}

func runCompare(args []string) {
	fs := flag.NewFlagSet("diffreview", flag.ExitOnError)
	a := fs.String("a", "", "path to the first handoff parcel ZIP (or parcel id with -parcels)")
	b := fs.String("b", "", "path to the second handoff parcel ZIP (or parcel id with -parcels)")
	aID := fs.String("a-id", "", "SHA-256 id of parcel A in -parcels store")
	bID := fs.String("b-id", "", "SHA-256 id of parcel B in -parcels store")
	store := fs.String("parcels", "", "parcel store directory (<id>.zip) for id references")
	out := fs.String("out", "var/diff-review", "directory for the difference listening-review artifact")
	changeRMS := fs.Float64("change-rms", 0, "normalized RMS threshold confirming content change (default 0.05)")
	changePeak := fs.Float64("change-peak", 0, "normalized peak threshold confirming content change (default 0.20)")
	sameRMS := fs.Float64("same-rms", 0, "normalized RMS ceiling for confident agreement (default 0.006)")
	samePeak := fs.Float64("same-peak", 0, "normalized peak ceiling for confident agreement (default 0.03)")
	minSpan := fs.Float64("min-span", 0, "minimum jointly-readable seconds for a same verdict (default 0.02)")
	pad := fs.Float64("pad", 0, "clip handle padding seconds (default 0.12)")
	mergeGap := fs.Float64("merge-gap", 0, "bridge equivalent gaps up to this many seconds when grouping regions")
	_ = fs.Parse(args)

	aRef := pickRef(*a, *aID)
	bRef := pickRef(*b, *bID)
	outDir := *out
	// Positional form: diffreview <a.zip|id> <b.zip|id> [out-dir]
	if fs.NArg() >= 2 {
		rest := fs.Args()
		if aRef == "" {
			aRef = rest[0]
		}
		if bRef == "" {
			bRef = rest[1]
		}
		if fs.NArg() >= 3 {
			outDir = rest[2]
		}
	}
	if aRef == "" || bRef == "" {
		fmt.Fprintln(os.Stderr, "error: two parcels are required: -a A.zip -b B.zip [-out dir], or positional: diffreview A.zip B.zip [out]")
		fs.Usage()
		os.Exit(2)
	}
	opt := diffreview.DefaultOptions()
	if *changeRMS > 0 {
		opt.ChangeRMS = *changeRMS
	}
	if *changePeak > 0 {
		opt.ChangePeak = *changePeak
	}
	if *sameRMS > 0 {
		opt.SameRMS = *sameRMS
	}
	if *samePeak > 0 {
		opt.SamePeak = *samePeak
	}
	if *minSpan > 0 {
		opt.MinSpanSeconds = *minSpan
	}
	if *pad > 0 {
		opt.PadSeconds = *pad
	}
	if *mergeGap > 0 {
		opt.MergeGapSeconds = *mergeGap
	}

	rep, files, err := BuildFromRefs(aRef, bRef, *store, outDir, opt)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	printSummary(rep, files)
}

func pickRef(path, id string) string {
	if path != "" {
		return path
	}
	return id
}

// BuildFromRefs resolves, verifies, compares and writes the artifact. It is
// separated so future entry points (examples or tests) can reuse the flow.
func BuildFromRefs(aRef, bRef, storeDir, outDir string, opt diffreview.Options) (*diffreview.Report, diffreview.ArtifactFiles, error) {
	aPath, aBytes, err := diffreview.ResolveParcelRef(aRef, storeDir)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	bPath, bBytes, err := diffreview.ResolveParcelRef(bRef, storeDir)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	// ID references are content-addressed: the bytes must hash to the named id,
	// matching parcel.Store.Read semantics.
	if diffreview.IsDigestID(aRef) && diffreview.SHA256Hex(aBytes) != aRef {
		return nil, diffreview.ArtifactFiles{}, fmt.Errorf("parcel id %s does not match bytes at %s", aRef, aPath)
	}
	if diffreview.IsDigestID(bRef) && diffreview.SHA256Hex(bBytes) != bRef {
		return nil, diffreview.ArtifactFiles{}, fmt.Errorf("parcel id %s does not match bytes at %s", bRef, bPath)
	}
	srcA, err := diffreview.LoadParcelBytes(aPath, aBytes)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	srcB, err := diffreview.LoadParcelBytes(bPath, bBytes)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	rep, err := diffreview.Compare(srcA, srcB, opt)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	files, err := diffreview.WriteArtifact(rep, outDir)
	if err != nil {
		return nil, diffreview.ArtifactFiles{}, err
	}
	return rep, files, nil
}

func runDemo(args []string) {
	fs := flag.NewFlagSet("diffreview demo", flag.ExitOnError)
	out := fs.String("out", "var/diff-demo", "demo output root (parcels/ fixtures and review/ artifact)")
	_ = fs.Parse(args)

	parcelDir := filepath.Join(*out, "parcels")
	reviewDir := filepath.Join(*out, "review")
	a, b, err := diffreview.BuildDemoParcels(parcelDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: build demo parcels:", err)
		os.Exit(2)
	}
	rep, files, err := BuildFromRefs(a, b, "", reviewDir, diffreview.DefaultOptions())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stderr, "demo parcels:\n  A %s\n  B %s\n", a, b)
	printSummary(rep, files)
}

func printSummary(rep *diffreview.Report, files diffreview.ArtifactFiles) {
	out := map[string]any{
		"schema":        rep.Schema,
		"report_json":   files.ReportJSON,
		"report_md":     files.Markdown,
		"report_html":   files.HTML,
		"review_wav":    files.MasterWAV,
		"bundle_zip":    files.BundleZip,
		"bundle_sha256": files.ZipSHA256,
		"out_dir":       files.Dir,
		"overlap":       rep.Overlap,
		"summary":       rep.Summary,
		"regions":       regionBrief(rep),
		"sources": []map[string]string{
			{"role": "a", "parcel_id": rep.Sources[0].ParcelID, "path": rep.Sources[0].GivenPath},
			{"role": "b", "parcel_id": rep.Sources[1].ParcelID, "path": rep.Sources[1].GivenPath},
		},
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func regionBrief(rep *diffreview.Report) []map[string]any {
	out := make([]map[string]any, 0, len(rep.Regions))
	for _, r := range rep.Regions {
		out = append(out, map[string]any{
			"id": r.ID, "mark": r.Mark, "mark_zh": r.MarkZh,
			"frames": r.Frames, "also_marks": r.AlsoMarks,
			"span_frame_start": r.Span.FrameStart, "span_frame_end": r.Span.FrameEnd,
			"cue_a": r.Cues.A.PCMFrame, "cue_b": r.Cues.B.PCMFrame,
			"compare_wav": r.Files.CompareWAV,
		})
	}
	return out
}
