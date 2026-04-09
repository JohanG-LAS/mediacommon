package h264

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrAnnexBNoNALUs is returned by AnnexBUnmarshal when no NALUs have been decoded.
var ErrAnnexBNoNALUs = errors.New("Annex-B unit doesn't contain any NALU")

// ErrAnnexBNoInitialDelimiter is returned by AnnexBUnmarshal when the initial delimiter is not found.
var ErrAnnexBNoInitialDelimiter = errors.New("initial delimiter not found")

// AnnexB is an access unit that can be decoded/encoded from/to the Annex-B stream format.
// Specification: ITU-T Rec. H.264, Annex B
type AnnexB [][]byte

// Unmarshal decodes an access unit from the Annex-B stream format.
func (a *AnnexB) Unmarshal(buf []byte) error {
	var pos int
	switch {
	case len(buf) >= 4 && buf[0] == 0x00 && buf[1] == 0x00 && buf[2] == 0x00 && buf[3] == 0x01:
		pos = 4
	case len(buf) >= 3 && buf[0] == 0x00 && buf[1] == 0x00 && buf[2] == 0x01:
		pos = 3
	default:
		return ErrAnnexBNoInitialDelimiter
	}

	if len(buf) == pos {
		return ErrAnnexBNoNALUs
	}

	type naluPos struct {
		start int
		end   int
	}

	var posArr [MaxNALUsPerAccessUnit]naluPos
	posCount := 0
	auSize := 0

	for pos < len(buf) {
		i := bytes.Index(buf[pos:], []byte{0x00, 0x00, 0x01})

		if i == -1 {
			remaining := len(buf) - pos
			if remaining > 0 {
				if (auSize + remaining) > MaxAccessUnitSize {
					return fmt.Errorf("access unit size (%d) is too big, maximum is %d", auSize+remaining, MaxAccessUnitSize)
				}
				if posCount >= MaxNALUsPerAccessUnit {
					return fmt.Errorf("NALU count (%d) exceeds maximum allowed (%d)",
						posCount+1, MaxNALUsPerAccessUnit)
				}
				posArr[posCount] = naluPos{start: pos, end: len(buf)}
				posCount++
			}
			break
		}

		var naluEnd int
		if i > 0 && buf[pos+i-1] == 0x00 {
			naluEnd = pos + i - 1
		} else {
			naluEnd = pos + i
		}

		naluSize := naluEnd - pos
		if naluSize > 0 {
			auSize += naluSize
			if auSize > MaxAccessUnitSize {
				return fmt.Errorf("access unit size (%d) is too big, maximum is %d", auSize, MaxAccessUnitSize)
			}
			if posCount >= MaxNALUsPerAccessUnit {
				return fmt.Errorf("NALU count (%d) exceeds maximum allowed (%d)",
					posCount+1, MaxNALUsPerAccessUnit)
			}
			posArr[posCount] = naluPos{start: pos, end: naluEnd}
			posCount++
		}

		pos += i + 3
	}

	if posCount == 0 {
		return ErrAnnexBNoNALUs
	}

	*a = make([][]byte, posCount)
	for i := range posCount {
		(*a)[i] = buf[posArr[i].start:posArr[i].end]
	}

	return nil
}

func (a AnnexB) marshalSize() int {
	n := 0
	for _, nalu := range a {
		n += 4 + len(nalu)
	}
	return n
}

// Marshal encodes an access unit into the Annex-B stream format.
func (a AnnexB) Marshal() ([]byte, error) {
	buf := make([]byte, a.marshalSize())
	pos := 0

	for _, nalu := range a {
		pos += copy(buf[pos:], []byte{0x00, 0x00, 0x00, 0x01})
		pos += copy(buf[pos:], nalu)
	}

	return buf, nil
}
