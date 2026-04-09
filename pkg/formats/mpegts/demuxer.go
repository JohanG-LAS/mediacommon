package mpegts

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	packetSize = 188
	syncByte   = 0x47
)

// pesStream accumulates PES payload for a single PID.
type pesStream struct {
	buf     []byte
	pos     int
	header  pesHeader
	started bool
	cc      uint8 // last continuity counter (0-15)
	ccValid bool  // whether cc has been set (first packet on this PID)
}

func (s *pesStream) reset() {
	s.pos = 0
	s.started = false
	s.header = pesHeader{}
}

// checkCC validates the continuity counter. Returns true if the CC is
// sequential (or this is the first packet). Updates the stored CC.
func (s *pesStream) checkCC(cc uint8) bool {
	if !s.ccValid {
		s.cc = cc
		s.ccValid = true
		return true
	}
	expected := (s.cc + 1) & 0x0F
	s.cc = cc
	return cc == expected
}

// demuxerResult is the output of a single demuxer step.
// Embedded in the demuxer to avoid per-call allocation.
type demuxerResult struct {
	pid       uint16
	hasPES    bool
	pesData   []byte
	streamID  uint8
	hasPTS    bool
	pts       int64
	hasDTS    bool
	dts       int64
	hasPMT    bool
	pmt       *pmtData
	lastPTS   int64
	hasLastPTS bool
}

// demuxer is a zero-alloc MPEG-TS demuxer that replaces go-astits for the
// reader path.
//
// Design:
//   - Reads are done in bulk (7 packets) then individual packets are parsed
//     from the read buffer without allocation.
//   - PES payload is accumulated per-PID into reusable buffers. On
//     payload_unit_start of the next PES for that PID, the previous PES is
//     flushed and its header parsed in-place.
//   - The result struct is embedded and reused across calls.
type demuxer struct {
	r             io.Reader
	onDecodeError func(error)

	// Bulk read buffer (7 × 188 = 1316 bytes, matching preDemuxer)
	readBuf    [7 * packetSize]byte
	readBufLen int
	readBufPos int

	// Aligned single-packet buffer
	pktBuf    [packetSize]byte
	pktBufPos int // how many bytes filled into pktBuf so far

	// PAT/PMT state
	pmtPID   uint16
	pmtFound bool

	// PSI section accumulation for PAT and PMT
	patSection []byte
	pmtSection []byte

	// PES streams keyed by PID
	streams map[uint16]*pesStream

	// Last intercepted PTS (for async KLV)
	lastPTS    int64
	hasLastPTS bool

	// Reused return value
	result demuxerResult
}

func (d *demuxer) initialize() {
	if d.onDecodeError == nil {
		d.onDecodeError = func(_ error) {}
	}
	d.streams = make(map[uint16]*pesStream)
	d.pktBufPos = 0
	d.readBufLen = 0
	d.readBufPos = 0
	d.pmtPID = 0
	d.pmtFound = false
}

// readAligned reads the next sync-aligned 188-byte TS packet into d.pktBuf.
func (d *demuxer) readAligned() error {
	for {
		// Fill pktBuf from readBuf
		for d.pktBufPos < packetSize {
			if d.readBufPos >= d.readBufLen {
				n, err := d.r.Read(d.readBuf[:])
				if n == 0 && err != nil {
					if err == io.EOF || err == io.ErrUnexpectedEOF {
						return io.EOF
					}
					return err
				}
				d.readBufPos = 0
				d.readBufLen = n
			}

			copied := copy(d.pktBuf[d.pktBufPos:], d.readBuf[d.readBufPos:d.readBufLen])
			d.readBufPos += copied
			d.pktBufPos += copied
		}

		// We have a full 188-byte buffer. Check sync.
		skipped := 0
		for skipped < packetSize && d.pktBuf[skipped] != syncByte {
			skipped++
		}

		if skipped == 0 {
			d.pktBufPos = 0
			return nil
		}

		d.onDecodeError(fmt.Errorf("skipped %d bytes", skipped))

		// Shift remaining bytes to front and continue filling
		remaining := packetSize - skipped
		copy(d.pktBuf[:remaining], d.pktBuf[skipped:])
		d.pktBufPos = remaining
	}
}

// flushPES parses the accumulated PES and populates d.result.
// The stream's buffer is detached and given to the result so the caller
// owns the data. The stream will allocate a new buffer on its next use.
func (d *demuxer) flushPES(stream *pesStream, pid uint16) {
	data := stream.buf[:stream.pos]
	if len(data) == 0 {
		stream.reset()
		return
	}

	// Detach buffer from stream — the result takes ownership
	stream.buf = nil
	stream.pos = 0

	h, err := parsePESHeader(data)
	if err != nil {
		d.onDecodeError(err)
		stream.started = false
		return
	}

	if h.dataOffset > len(data) {
		d.onDecodeError(fmt.Errorf("PES data offset exceeds buffer"))
		stream.started = false
		return
	}

	d.result.pid = pid
	d.result.hasPES = true
	d.result.pesData = data[h.dataOffset:]
	d.result.streamID = h.streamID
	d.result.hasPTS = h.hasPTS
	d.result.pts = h.pts
	d.result.hasDTS = h.hasDTS
	d.result.dts = h.dts
	d.result.hasPMT = false
	d.result.pmt = nil
	d.result.hasLastPTS = d.hasLastPTS
	d.result.lastPTS = d.lastPTS

	if h.hasPTS {
		d.hasLastPTS = true
		d.lastPTS = h.pts
	}

	stream.header = h
}

// nextData returns the next demuxed result (PES or PMT).
// The returned pointer is to an embedded struct and is only valid until the
// next call.
func (d *demuxer) nextData() (*demuxerResult, error) {
	for {
		err := d.readAligned()
		if err != nil {
			// Before returning EOF, flush any remaining PES streams
			for pid, stream := range d.streams {
				if stream.started && stream.pos > 0 {
					d.result = demuxerResult{}
					d.flushPES(stream, pid)
					stream.reset()
					if d.result.hasPES {
						// We need to come back for more streams, but
						// the next readAligned will return EOF again.
						return &d.result, nil
					}
				}
			}
			return nil, err
		}

		pkt := d.pktBuf[:]

		// Parse TS header — ISO 13818-1, Section 2.4.3.2, Table 2-2
		pid := binary.BigEndian.Uint16(pkt[1:3]) & 0x1FFF
		payloadStart := pkt[1]&0x40 != 0
		hasAdaptation := pkt[3]&0x20 != 0
		hasPayload := pkt[3]&0x10 != 0

		payloadPos := 4
		if hasAdaptation {
			afLen := int(pkt[4])
			payloadPos += 1 + afLen
			if payloadPos > packetSize {
				d.onDecodeError(fmt.Errorf("adaptation field length exceeds packet"))
				continue
			}
		}

		if !hasPayload {
			continue
		}

		payload := pkt[payloadPos:]
		if len(payload) == 0 {
			continue
		}

		// PAT (PID 0)
		if pid == 0 {
			if payloadStart {
				pointerField := int(payload[0])
				sectionStart := 1 + pointerField
				if sectionStart < len(payload) {
					d.patSection = append(d.patSection[:0], payload[sectionStart:]...)
				}
			} else if len(d.patSection) > 0 {
				d.patSection = append(d.patSection, payload...)
			}

			if len(d.patSection) > 0 && !d.pmtFound {
				pmtPID, parseErr := parsePAT(d.patSection)
				if parseErr == nil {
					d.pmtPID = pmtPID
				}
			}
			continue
		}

		// PMT
		if d.pmtPID != 0 && pid == d.pmtPID {
			if payloadStart {
				pointerField := int(payload[0])
				sectionStart := 1 + pointerField
				if sectionStart < len(payload) {
					d.pmtSection = append(d.pmtSection[:0], payload[sectionStart:]...)
				}
			} else if len(d.pmtSection) > 0 {
				d.pmtSection = append(d.pmtSection, payload...)
			}

			if len(d.pmtSection) > 0 {
				pmt, parseErr := parsePMT(d.pmtSection)
				if parseErr == nil {
					d.pmtFound = true
					d.result = demuxerResult{
						pid:    pid,
						hasPMT: true,
						pmt:    pmt,
					}
					return &d.result, nil
				}
			}
			continue
		}

		// PES streams

		// Null packets (PID 0x1FFF) carry no data — skip them.
		if pid == 0x1FFF {
			continue
		}

		stream, ok := d.streams[pid]
		if !ok {
			stream = &pesStream{}
			d.streams[pid] = stream
		}

		cc := pkt[3] & 0x0F
		ccOK := stream.checkCC(cc)

		if payloadStart {
			// Verify this is actually a PES start (0x00 0x00 0x01 prefix).
			// PIDs carrying PSI tables (SDT/NIT/EIT, etc.) also set
			// payload_unit_start_indicator but use PSI format with a
			// pointer_field byte, not a PES start code. Accumulating them
			// would cause spurious "invalid PES start code" errors on the
			// next periodic retransmission of the same table. Silently
			// discard and reset any previously accumulated data.
			if len(payload) < 3 || payload[0] != 0x00 || payload[1] != 0x00 || payload[2] != 0x01 {
				stream.reset()
				continue
			}

			// If we already have accumulated data, flush the previous PES
			if stream.started && stream.pos > 0 {
				if !ccOK {
					// CC discontinuity: packets were lost, so the
					// accumulated PES is incomplete. Silently discard
					// it (matching go-astits behaviour) and start fresh.
					stream.reset()
					stream.started = true
					needed := len(payload)
					stream.buf = make([]byte, needed*2)
					stream.pos = copy(stream.buf[:needed], payload)
					continue
				}

				d.result = demuxerResult{}
				d.flushPES(stream, pid)

				// Start new PES (flushPES detached the old buffer)
				stream.started = true
				stream.buf = make([]byte, len(payload)*2)
				stream.pos = copy(stream.buf[:len(payload)], payload)

				if d.result.hasPES {
					return &d.result, nil
				}
				continue
			}

			stream.reset()
			stream.started = true

			needed := len(payload)
			if needed > cap(stream.buf) {
				stream.buf = make([]byte, needed*2)
			} else if needed > len(stream.buf) {
				stream.buf = stream.buf[:cap(stream.buf)]
			}
			stream.pos = copy(stream.buf[:needed], payload)
			continue
		}

		// Continuation packet
		if !stream.started {
			continue
		}

		if !ccOK {
			// CC discontinuity mid-PES: accumulated data is incomplete.
			// Discard and wait for the next payload_unit_start.
			stream.reset()
			continue
		}

		needed := stream.pos + len(payload)
		if needed > cap(stream.buf) {
			newBuf := make([]byte, needed*2)
			copy(newBuf, stream.buf[:stream.pos])
			stream.buf = newBuf
		} else if needed > len(stream.buf) {
			stream.buf = stream.buf[:cap(stream.buf)]
		}
		stream.pos += copy(stream.buf[stream.pos:needed], payload)
	}
}
