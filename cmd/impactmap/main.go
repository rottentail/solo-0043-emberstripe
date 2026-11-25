// Command impactmap builds a lineage-aware cross-scan damage handoff.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/impactmap"
)

func main() {
	scans := flag.String("scans", "var/scans", "catalog directory containing both scanned reels")
	surveyPath := flag.String("survey", "", "technician survey JSON file, or - for stdin")
	outPath := flag.String("out", "", "write machine JSON to this file (default stdout)")
	markdownPath := flag.String("markdown", "", "write Chinese Markdown handoff to this file")
	pretty := flag.Bool("pretty", true, "indent JSON output")
	flag.Parse()

	if *surveyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: impactmap -survey survey.json -scans var/scans [-out map.json] [-markdown map.md]")
		os.Exit(2)
	}
	var (
		data []byte
		err  error
	)
	if *surveyPath == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	} else {
		data, err = os.ReadFile(*surveyPath)
	}
	if err != nil {
		fatal(err)
	}
	catalog, err := capture.LoadCatalog(*scans)
	if err != nil {
		fatal(err)
	}
	result, err := build(catalog, data)
	if err != nil {
		fatal(err)
	}
	jsonBytes, err := encode(result, *pretty)
	if err != nil {
		fatal(err)
	}
	if *outPath == "" {
		if _, err := os.Stdout.Write(jsonBytes); err != nil {
			fatal(err)
		}
	} else if err := os.WriteFile(*outPath, jsonBytes, 0o640); err != nil {
		fatal(err)
	}
	if *markdownPath != "" {
		if err := os.WriteFile(*markdownPath, impactmap.RenderMarkdown(result), 0o640); err != nil {
			fatal(err)
		}
	}
}

func build(catalog map[string]*capture.Reel, data []byte) (*impactmap.ImpactMap, error) {
	survey, err := impactmap.ParseSurvey(data)
	if err != nil {
		return nil, err
	}
	return impactmap.Build(catalog, survey)
}

func encode(v any, pretty bool) ([]byte, error) {
	if pretty {
		return json.MarshalIndent(v, "", "  ")
	}
	return json.Marshal(v)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "impactmap:", err)
	os.Exit(1)
}
