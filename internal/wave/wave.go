// Package wave writes and inspects the fixed PCM interchange used by the station.
package wave

import (
	"encoding/binary"
	"errors"
)

const headerBytes = 44
const blockBytes = 4

// HeaderBytes is the fixed 44-byte RIFF/WAVE header of the interchange.
const HeaderBytes = headerBytes

// BlockBytes is one interleaved 16-bit stereo frame (two channels × two bytes).
const BlockBytes = blockBytes

type Header struct {
	Rate     int `json:"rate"`
	Frames   int `json:"sample_frames"`
	Channels int `json:"channels"`
}

func Encode(rate int, samples []int16) []byte {
	b := make([]byte, headerBytes+2*len(samples))
	le := binary.LittleEndian
	copy(b, "RIFF")
	le.PutUint32(b[4:], uint32(len(b)-8))
	copy(b[8:], "WAVEfmt ")
	le.PutUint32(b[16:], 16)
	le.PutUint16(b[20:], 1)
	le.PutUint16(b[22:], 2)
	le.PutUint32(b[24:], uint32(rate))
	le.PutUint32(b[28:], uint32(rate*blockBytes))
	le.PutUint16(b[32:], blockBytes)
	le.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	le.PutUint32(b[40:], uint32(len(b)-headerBytes))
	for i, v := range samples {
		le.PutUint16(b[headerBytes+i*2:], uint16(v))
	}
	return b
}

func Inspect(b []byte) (Header, error) {
	le := binary.LittleEndian
	if len(b) < headerBytes || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" || int(le.Uint32(b[4:])) != len(b)-8 || int(le.Uint32(b[40:])) != len(b)-headerBytes || (len(b)-headerBytes)%blockBytes != 0 {
		return Header{}, errors.New("invalid PCM container")
	}
	rate := int(le.Uint32(b[24:]))
	if le.Uint32(b[16:]) != 16 || le.Uint16(b[20:]) != 1 || le.Uint16(b[22:]) != 2 || le.Uint16(b[32:]) != blockBytes || le.Uint16(b[34:]) != 16 || rate < 8000 || rate > 96000 || int(le.Uint32(b[28:])) != rate*blockBytes {
		return Header{}, errors.New("unexpected PCM format")
	}
	return Header{Rate: rate, Frames: (len(b) - headerBytes) / blockBytes, Channels: 2}, nil
}
