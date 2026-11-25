package periodicity

import (
	"github.com/rottentail/solo-0043-emberstripe/internal/capture"
	"github.com/rottentail/solo-0043-emberstripe/internal/register"
)

// Verify runs the periodic-interference check over one picture window. The
// reference band stays outside both sound lanes; the three series share the
// source clock and the same missing/outside coverage.
func Verify(reel *capture.Reel, w register.Window, band capture.Span) (Report, error) {
	if !ValidBand(reel, band) {
		return Report{}, ErrInvalidBand
	}
	rate := register.Rate(reel.Header)
	kinds := []SeriesKind{Edge, Left, Right}
	series := make(map[SeriesKind][]float64, len(kinds))
	segments := make(map[SeriesKind][]Segment, len(kinds))
	spectra := make([]Spectrum, 0, len(kinds))
	for _, kind := range kinds {
		values, parts, err := BuildSeries(reel, w, kind, band)
		if err != nil {
			return Report{}, err
		}
		series[kind] = values
		segments[kind] = parts
		spectra = append(spectra, Analyze(kind, values, parts, rate, w.Start*reel.Rows))
	}
	spectrumByKind := make(map[SeriesKind]Spectrum, len(spectra))
	for _, spectrum := range spectra {
		spectrumByKind[spectrum.Kind] = spectrum
	}
	edgeSpectrum := spectrumByKind[Edge]
	leftSpectrum := spectrumByKind[Left]
	rightSpectrum := spectrumByKind[Right]
	leftSpectrum.AlignWith(&edgeSpectrum)
	rightSpectrum.AlignWith(&edgeSpectrum)
	spectrumByKind[Left] = leftSpectrum
	spectrumByKind[Right] = rightSpectrum
	spectra[1] = leftSpectrum
	spectra[2] = rightSpectrum
	pairs := []Pair{
		comparePair(edgeSpectrum, leftSpectrum),
		comparePair(edgeSpectrum, rightSpectrum),
	}
	report := Report{
		Band:       captureSpan{X0: band.X0, X1: band.X1},
		Rate:       rate,
		Rows:       reel.Rows,
		Window:     windowShape{Start: w.Start, End: w.End},
		Spectra:    spectra,
		Pairs:      pairs,
		Overall:    overall(pairs),
		Unreadable: coverageBlocks(reel, w),
	}
	length := (w.End - w.Start) * reel.Rows
	for _, block := range report.Unreadable {
		switch block.Status {
		case register.Missing:
			report.Missing += block.EndRow - block.StartRow
		case register.Outside:
			report.Outside += block.EndRow - block.StartRow
		}
	}
	report.Readable = length - report.Missing - report.Outside
	return report, nil
}

// coverageBlocks records every non-readable run with its original source
// position, preserving missing middle pages for later re-scans.
func coverageBlocks(reel *capture.Reel, w register.Window) []Block {
	blocks := make([]Block, 0)
	for _, run := range register.Coverage(reel, w) {
		if run.Status == register.Readable {
			continue
		}
		block := Block{StartRow: run.Start, EndRow: run.End, Status: run.Status}
		source := run.Start + reel.Lead
		if source >= 0 && source < reel.Rows*reel.Frames {
			block.Frame = source / reel.Rows
			block.Row = source % reel.Rows
		} else {
			block.Frame = -1
			block.Row = source
		}
		blocks = append(blocks, block)
	}
	return blocks
}
