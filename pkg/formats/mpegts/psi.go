package mpegts

import (
	"encoding/binary"
	"fmt"
)

// Stream type constants — ISO 13818-1, Table 2-34
const (
	streamTypeH265Video    = 0x24
	streamTypeH264Video    = 0x1B
	streamTypeMPEG4Video   = 0x10
	streamTypeMPEG2Video   = 0x02
	streamTypeMPEG1Video   = 0x01
	streamTypeAACAudio     = 0x0F
	streamTypeAACLATMAudio = 0x11
	streamTypeMPEG1Audio   = 0x03
	streamTypeAC3Audio     = 0x81
	streamTypeEAC3Audio    = 0x87
	streamTypePrivateData  = 0x06
	streamTypeMetadata     = 0x15
)

// Descriptor tag constants — ISO 13818-1 / ETSI EN 300 468
const (
	descriptorTagRegistration = 0x05
	descriptorTagExtension    = 0x7F
	descriptorTagSubtitling   = 0x59
)

// pmtElementaryStream represents a single elementary stream entry from a PMT.
type pmtElementaryStream struct {
	streamType  uint8
	pid         uint16
	descriptors []byte // raw descriptor bytes
}

// pmtData holds the parsed result of a PMT section.
type pmtData struct {
	elementaryStreams []pmtElementaryStream
}

// parsePAT parses a PAT section payload (after the pointer field) and returns
// the PID of the first PMT.
// ISO 13818-1, Section 2.4.4.3, Table 2-25
func parsePAT(section []byte) (uint16, error) {
	// Minimum PAT section: 8 bytes header + 4 bytes CRC = 12 bytes
	// Plus at least one 4-byte program entry = 16 bytes
	if len(section) < 12 {
		return 0, fmt.Errorf("PAT section too short: %d bytes", len(section))
	}

	tableID := section[0]
	if tableID != 0x00 {
		return 0, fmt.Errorf("not a PAT: table_id=0x%02x", tableID)
	}

	sectionLength := int(binary.BigEndian.Uint16(section[1:3]) & 0x0FFF)
	if sectionLength < 9 {
		return 0, fmt.Errorf("PAT section_length too short: %d", sectionLength)
	}

	// Skip: transport_stream_id(2), version/current(1), section_number(1), last_section_number(1)
	// Program entries start at offset 8, end at 3 + sectionLength - 4 (excluding CRC)
	entriesEnd := 3 + sectionLength - 4
	if entriesEnd > len(section) {
		entriesEnd = len(section) - 4
	}

	for pos := 8; pos+4 <= entriesEnd; pos += 4 {
		programNumber := binary.BigEndian.Uint16(section[pos : pos+2])
		pid := binary.BigEndian.Uint16(section[pos+2:pos+4]) & 0x1FFF

		if programNumber != 0 {
			return pid, nil
		}
	}

	return 0, fmt.Errorf("no program found in PAT")
}

// parsePMT parses a PMT section payload (after the pointer field).
// ISO 13818-1, Section 2.4.4.8, Table 2-28
func parsePMT(section []byte) (*pmtData, error) {
	if len(section) < 12 {
		return nil, fmt.Errorf("PMT section too short: %d bytes", len(section))
	}

	tableID := section[0]
	if tableID != 0x02 {
		return nil, fmt.Errorf("not a PMT: table_id=0x%02x", tableID)
	}

	sectionLength := int(binary.BigEndian.Uint16(section[1:3]) & 0x0FFF)
	sectionEnd := 3 + sectionLength - 4 // exclude CRC
	if sectionEnd > len(section)-4 {
		sectionEnd = len(section) - 4
	}

	// Skip: program_number(2), version/current(1), section_number(1), last_section_number(1)
	// PCR_PID at offset 8-9 (skip)
	programInfoLength := int(binary.BigEndian.Uint16(section[10:12]) & 0x0FFF)
	pos := 12 + programInfoLength

	if pos > sectionEnd {
		return nil, fmt.Errorf("PMT program_info_length exceeds section")
	}

	var streams []pmtElementaryStream

	for pos+5 <= sectionEnd {
		st := section[pos]
		ePID := binary.BigEndian.Uint16(section[pos+1:pos+3]) & 0x1FFF
		esInfoLength := int(binary.BigEndian.Uint16(section[pos+3:pos+5]) & 0x0FFF)
		pos += 5

		descEnd := pos + esInfoLength
		if descEnd > sectionEnd {
			descEnd = sectionEnd
		}

		var descriptors []byte
		if esInfoLength > 0 && pos < descEnd {
			descriptors = section[pos:descEnd]
		}

		streams = append(streams, pmtElementaryStream{
			streamType:  st,
			pid:         ePID,
			descriptors: descriptors,
		})

		pos = descEnd
	}

	return &pmtData{elementaryStreams: streams}, nil
}

// Descriptor iteration helpers — work on raw descriptor bytes.
// Each descriptor: tag(1) + length(1) + data(length).

// findRegistrationID searches raw descriptors for a Registration descriptor
// (tag 0x05) and returns its format_identifier (4 bytes, big-endian uint32).
func findRegistrationID(descriptors []byte) (uint32, bool) {
	ret := uint32(0)

	for pos := 0; pos+2 <= len(descriptors); {
		tag := descriptors[pos]
		length := int(descriptors[pos+1])
		pos += 2

		if pos+length > len(descriptors) {
			break
		}

		if tag == descriptorTagRegistration && length >= 4 {
			id := binary.BigEndian.Uint32(descriptors[pos : pos+4])
			if ret != 0 {
				return 0, false
			}
			ret = id
		}

		pos += length
	}

	if ret == 0 {
		return 0, false
	}

	return ret, true
}

// findExtensionDescriptorData searches raw descriptors for an Extension
// descriptor (tag 0x7F) with the given extension tag. Returns the data
// bytes after the extension tag byte.
func findExtensionDescriptorData(descriptors []byte, extensionTag uint8) ([]byte, bool) {
	for pos := 0; pos+2 <= len(descriptors); {
		tag := descriptors[pos]
		length := int(descriptors[pos+1])
		pos += 2

		if pos+length > len(descriptors) {
			break
		}

		if tag == descriptorTagExtension && length >= 1 && descriptors[pos] == extensionTag {
			return descriptors[pos+1 : pos+length], true
		}

		pos += length
	}

	return nil, false
}

// findMetadataDescriptorData searches raw descriptors for a Metadata
// descriptor (tag 0x26) and returns its body bytes.
func findMetadataDescriptorData(descriptors []byte) [][]byte {
	var results [][]byte

	for pos := 0; pos+2 <= len(descriptors); {
		tag := descriptors[pos]
		length := int(descriptors[pos+1])
		pos += 2

		if pos+length > len(descriptors) {
			break
		}

		if tag == substructsDescriptorTagMetadata {
			results = append(results, descriptors[pos:pos+length])
		}

		pos += length
	}

	return results
}

// substructsDescriptorTagMetadata matches substructs.DescriptorTagMetadata
const substructsDescriptorTagMetadata = 0x26

// findSubtitlingItems searches raw descriptors for a Subtitling descriptor
// (tag 0x59) and parses its items. Returns nil if not found.
// ETSI EN 300 468, Section 6.2.41
func findSubtitlingItems(descriptors []byte) []subtitlingItem {
	for pos := 0; pos+2 <= len(descriptors); {
		tag := descriptors[pos]
		length := int(descriptors[pos+1])
		pos += 2

		if pos+length > len(descriptors) {
			break
		}

		if tag == descriptorTagSubtitling && length >= 8 {
			return parseSubtitlingItems(descriptors[pos : pos+length])
		}

		pos += length
	}

	return nil
}

type subtitlingItem struct {
	Language          []byte
	Type              uint8
	CompositionPageID uint16
	AncillaryPageID   uint16
}

func parseSubtitlingItems(data []byte) []subtitlingItem {
	var items []subtitlingItem

	for pos := 0; pos+8 <= len(data); pos += 8 {
		items = append(items, subtitlingItem{
			Language:          data[pos : pos+3],
			Type:              data[pos+3],
			CompositionPageID: binary.BigEndian.Uint16(data[pos+4 : pos+6]),
			AncillaryPageID:   binary.BigEndian.Uint16(data[pos+6 : pos+8]),
		})
	}

	return items
}
