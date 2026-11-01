package core

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Frame kinds of the framed transport.
const (
	kindData   byte = 0 // payload bytes (channel 0: one JSON-RPC message)
	kindCredit byte = 1 // viewer -> core: u32 LE bytes consumed
	kindClose  byte = 2 // end of a stream
	kindResize byte = 3 // viewer -> core: u16 LE cols, u16 LE rows
	kindFocus  byte = 4 // viewer -> core: 1 byte, 1 gained / 0 lost
	kindFacts  byte = 5 // viewer -> core: JSON Display
)

const (
	headerLen    = 9
	maxFrameSize = 16 << 20
)

// errFrameTooLarge reports a frame payload above the protocol limit.
var errFrameTooLarge = errors.New("herdr-core: frame payload exceeds 16 MiB")

// frame is one decoded transport frame.
type frame struct {
	channel uint32
	kind    byte
	payload []byte
}

// appendFrame appends the wire encoding of a frame to dst.
func appendFrame(dst []byte, channel uint32, kind byte, payload []byte) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, channel)
	dst = append(dst, kind)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(payload)))
	return append(dst, payload...)
}

// encodeFrame returns the wire encoding of a frame.
func encodeFrame(channel uint32, kind byte, payload []byte) []byte {
	return appendFrame(make([]byte, 0, headerLen+len(payload)), channel, kind, payload)
}

// readFrame reads exactly one frame from r.
func readFrame(r io.Reader) (frame, error) {
	var hdr [headerLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return frame{}, err
	}
	n := binary.LittleEndian.Uint32(hdr[5:9])
	if n > maxFrameSize {
		return frame{}, fmt.Errorf("%w (%d bytes)", errFrameTooLarge, n)
	}
	f := frame{channel: binary.LittleEndian.Uint32(hdr[0:4]), kind: hdr[4]}
	if n > 0 {
		f.payload = make([]byte, n)
		if _, err := io.ReadFull(r, f.payload); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return frame{}, err
		}
	}
	return f, nil
}

func creditPayload(n int) []byte {
	return binary.LittleEndian.AppendUint32(nil, uint32(n))
}

func resizePayload(cols, rows int) []byte {
	b := binary.LittleEndian.AppendUint16(nil, clampU16(cols))
	return binary.LittleEndian.AppendUint16(b, clampU16(rows))
}

func clampU16(v int) uint16 {
	switch {
	case v < 1:
		return 1
	case v > 0xffff:
		return 0xffff
	}
	return uint16(v)
}
