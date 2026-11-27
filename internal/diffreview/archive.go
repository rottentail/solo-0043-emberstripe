package diffreview

import (
	"archive/zip"
	"bytes"
	"io"
)

// zipEntries reads all entries of a ZIP blob into memory. Inputs have already
// passed parcel.Verify (which bounds sizes and entry names).
func zipEntries(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		buf, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out[f.Name] = buf
	}
	return out, nil
}
