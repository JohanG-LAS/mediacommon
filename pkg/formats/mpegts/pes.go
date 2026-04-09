package mpegts

import (
	"fmt"
)

// PES header parsing — ISO 13818-1, Section 2.4.3.6, Table 2-17
//
// PES packet layout:
//   Bytes 0-2: packet_start_code_prefix (0x000001)
//   Byte 3:    stream_id
//   Bytes 4-5: PES_packet_length
//   (optional header follows if stream_id is not padding/private_stream_2)
//   Byte 6:    flags ('10' marker_bits, PES_scrambling_control, etc.)
//   Byte 7:    flags (PTS_DTS_flags, ESCR_flag, etc.)
//   Byte 8:    PES_header_data_length
//   Bytes 9+:  optional fields (PTS, DTS, ...)

const (
	streamIDPaddingStream  = 0xBE
	streamIDPrivateStream2 = 0xBF
)

type pesHeader struct {
	streamID   uint8
	hasPTS     bool
	pts        int64
	hasDTS     bool
	dts        int64
	dataOffset int
}

func parsePESHeader(buf []byte) (pesHeader, error) {
	if len(buf) < 6 {
		return pesHeader{}, fmt.Errorf("PES packet too short: %d bytes", len(buf))
	}

	// packet_start_code_prefix
	if buf[0] != 0x00 || buf[1] != 0x00 || buf[2] != 0x01 {
		return pesHeader{}, fmt.Errorf("invalid PES start code")
	}

	streamID := buf[3]
	// PES_packet_length at buf[4:6] — not needed for parsing

	if streamID == streamIDPaddingStream || streamID == streamIDPrivateStream2 {
		return pesHeader{
			streamID:   streamID,
			dataOffset: 6,
		}, nil
	}

	if len(buf) < 9 {
		return pesHeader{}, fmt.Errorf("PES optional header too short: %d bytes", len(buf))
	}

	// Byte 7: PTS_DTS_flags are bits 7-6
	ptsDTSIndicator := (buf[7] >> 6) & 0x03
	headerDataLen := int(buf[8])
	dataOffset := 9 + headerDataLen

	if dataOffset > len(buf) {
		return pesHeader{}, fmt.Errorf("PES header data length exceeds packet: %d > %d", dataOffset, len(buf))
	}

	var h pesHeader
	h.streamID = streamID
	h.dataOffset = dataOffset

	switch ptsDTSIndicator {
	case 0x02: // PTS only
		if len(buf) < 14 {
			return pesHeader{}, fmt.Errorf("PES packet too short for PTS: %d bytes", len(buf))
		}
		h.hasPTS = true
		h.pts = parseClock(buf[9:14])
		h.hasDTS = false
		h.dts = h.pts

	case 0x03: // PTS and DTS
		if len(buf) < 19 {
			return pesHeader{}, fmt.Errorf("PES packet too short for PTS+DTS: %d bytes", len(buf))
		}
		h.hasPTS = true
		h.pts = parseClock(buf[9:14])
		h.hasDTS = true
		h.dts = parseClock(buf[14:19])
	}

	return h, nil
}

// parseClock extracts a 33-bit MPEG-TS timestamp from 5 bytes.
// ISO 13818-1, Section 2.4.3.6
func parseClock(b []byte) int64 {
	return int64((((uint64(b[0]) >> 1) & 0x7) << 30) | (uint64(b[1]) << 22) |
		(((uint64(b[2]) >> 1) & 0x7f) << 15) | (uint64(b[3]) << 7) | ((uint64(b[4]) >> 1) & 0x7f))
}
