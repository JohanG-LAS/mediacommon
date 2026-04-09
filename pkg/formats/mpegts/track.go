package mpegts

import (
	"fmt"

	"github.com/asticode/go-astits"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/ac3"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/eac3"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/substructs"
)

const (
	opusIdentifier = 'O'<<24 | 'p'<<16 | 'u'<<8 | 's'
	klvaIdentifier = 'K'<<24 | 'L'<<16 | 'V'<<8 | 'A'
)

// MISB ST 1402, Table 4
const (
	metadataApplicationFormatGeneral            = 0x0100
	metadataApplicationFormatGeographicMetadata = 0x0101
	metadataApplicationFormatAnnotationMetadata = 0x0102
	metadataApplicationFormatStillImageOnDemand = 0x0103
)

func findMPEG4AudioConfigDemux(dem *demuxer, pid uint16) (*mpeg4audio.AudioSpecificConfig, error) {
	for {
		data, err := dem.nextData()
		if err != nil {
			return nil, err
		}

		if !data.hasPES || data.pid != pid {
			continue
		}

		var adtsPkts mpeg4audio.ADTSPackets
		err = adtsPkts.Unmarshal(data.pesData)
		if err != nil {
			return nil, fmt.Errorf("unable to decode ADTS: %w", err)
		}

		pkt := adtsPkts[0]
		return &mpeg4audio.AudioSpecificConfig{
			Type:          pkt.Type,
			SampleRate:    pkt.SampleRate,
			ChannelConfig: pkt.ChannelConfig,
			ChannelCount:  pkt.ChannelCount, //nolint:staticcheck
		}, nil
	}
}

func findAC3ParametersDemux(dem *demuxer, pid uint16) (int, int, error) {
	for {
		data, err := dem.nextData()
		if err != nil {
			return 0, 0, err
		}

		if !data.hasPES || data.pid != pid {
			continue
		}

		var syncInfo ac3.SyncInfo
		err = syncInfo.Unmarshal(data.pesData)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid AC-3 frame: %w", err)
		}

		var bsi ac3.BSI
		err = bsi.Unmarshal(data.pesData[5:])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid AC-3 frame: %w", err)
		}

		return syncInfo.SampleRate(), bsi.ChannelCount(), nil
	}
}

func findEAC3ParametersDemux(dem *demuxer, pid uint16) (int, int, error) {
	for {
		data, err := dem.nextData()
		if err != nil {
			return 0, 0, err
		}

		if !data.hasPES || data.pid != pid {
			continue
		}

		var syncInfo eac3.SyncInfo
		err = syncInfo.Unmarshal(data.pesData)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid E-AC-3 frame: %w", err)
		}

		return syncInfo.SampleRate(), syncInfo.ChannelCount(), nil
	}
}

func findCodecFromES(dem *demuxer, es pmtElementaryStream) (codecs.Codec, error) {
	switch es.streamType {
	// video

	case streamTypeH265Video:
		return &codecs.H265{}, nil

	case streamTypeH264Video:
		return &codecs.H264{}, nil

	case streamTypeMPEG4Video:
		return &codecs.MPEG4Video{}, nil

	case streamTypeMPEG2Video, streamTypeMPEG1Video:
		return &codecs.MPEG1Video{}, nil

	// audio

	case streamTypeAACAudio:
		conf, err := findMPEG4AudioConfigDemux(dem, es.pid)
		if err != nil {
			return nil, err
		}

		return &codecs.MPEG4Audio{
			Config: *conf,
		}, nil

	case streamTypeAACLATMAudio:
		return &codecs.MPEG4AudioLATM{}, nil

	case streamTypeMPEG1Audio:
		return &codecs.MPEG1Audio{}, nil

	case streamTypeAC3Audio:
		sampleRate, channelCount, err := findAC3ParametersDemux(dem, es.pid)
		if err != nil {
			return nil, err
		}

		return &codecs.AC3{
			SampleRate:   sampleRate,
			ChannelCount: channelCount,
		}, nil

	case streamTypeEAC3Audio:
		sampleRate, channelCount, err := findEAC3ParametersDemux(dem, es.pid)
		if err != nil {
			return nil, err
		}

		return &codecs.EAC3{
			SampleRate:   sampleRate,
			ChannelCount: channelCount,
		}, nil

	// other

	case streamTypePrivateData:
		if id, ok := findRegistrationID(es.descriptors); ok {
			switch id {
			case opusIdentifier:
				data, found := findExtensionDescriptorData(es.descriptors, 0x80)
				if !found {
					return nil, fmt.Errorf("opus audio descriptor not found")
				}

				var oad substructs.OpusAudioDescriptor
				err := oad.Unmarshal(data)
				if err != nil {
					return nil, fmt.Errorf("invalid Opus audio descriptor: %w", err)
				}

				return &codecs.Opus{
					Desc:         &oad,
					ChannelCount: oad.ChannelCount(),
				}, nil

			case klvaIdentifier:
				return &codecs.KLV{
					Synchronous: false,
				}, nil
			}
		} else if items := findSubtitlingItems(es.descriptors); items != nil {
			astitsItems := make([]*astits.DescriptorSubtitlingItem, len(items))
			for i, item := range items {
				lang := make([]byte, len(item.Language))
				copy(lang, item.Language)
				astitsItems[i] = &astits.DescriptorSubtitlingItem{
					Language:          lang,
					Type:              item.Type,
					CompositionPageID: item.CompositionPageID,
					AncillaryPageID:   item.AncillaryPageID,
				}
			}

			return &codecs.DVBSubtitle{
				Items: astitsItems,
			}, nil
		}

	case streamTypeMetadata:
		metaDescs := findMetadataDescriptorData(es.descriptors)
		for _, descData := range metaDescs {
			var dm substructs.MetadataDescriptor
			err := dm.Unmarshal(descData)
			if err != nil {
				continue
			}

			if dm.MetadataFormatIdentifier == klvaIdentifier {
				return &codecs.KLV{
					Synchronous: true,
				}, nil
			}
		}
	}

	return &codecs.Unsupported{}, nil
}

// ac3ComponentType builds the DVB component_type byte for AC-3.
// Per ETSI EN 300 468, the AC3 descriptor uses a similar format to E-AC-3.
func ac3ComponentType(channels int, fullService bool) uint8 {
	var ct uint8

	// Set full_service_flag (bit 0)
	if fullService {
		ct |= 0x01
	}

	// Encode channel configuration in bits 3-1
	switch {
	case channels <= 2:
		ct |= (0x02 << 1) // 2ch stereo
	case channels <= 4:
		ct |= (0x05 << 1) // multichannel stereo
	default:
		ct |= (0x06 << 1) // multichannel surround (5.1, etc.)
	}

	return ct
}

// componentTypeFromConfig builds the DVB component_type byte.
// Per ETSI EN 300 468, table D.1:
// Bits 7-4: service_type_flag (0=complete main, 1=music/effects, etc.)
// Bits 3-1: number_of_channels mapping
// Bit 0: full_service_flag
//
// For E-AC-3, the component_type encodes channel configuration:
//
//	0x00-0x3F: Full service, complete main
//	Bits 2-0 encode channel config: 0=mono/stereo, 1=mono, 2=stereo, 3=2ch, etc.
func eac3ComponentType(channels int, fullService bool) uint8 {
	var ct uint8

	if fullService {
		ct |= 0x01
	}

	switch {
	case channels <= 2:
		ct |= (0x02 << 1) // 2ch stereo
	case channels <= 4:
		ct |= (0x05 << 1) // multichannel stereo
	default:
		ct |= (0x06 << 1) // multichannel surround (5.1, 7.1, etc.)
	}

	return ct
}

// Track is a MPEG-TS track.
type Track struct {
	PID   uint16
	Codec codecs.Codec

	isLeading  bool // Writer-only
	mp3Checked bool // Writer-only
}

func (t Track) marshal() (*astits.PMTElementaryStream, error) {
	switch c := t.Codec.(type) {
	// video

	case *codecs.H265:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeH265Video,
		}, nil

	case *codecs.H264:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeH264Video,
		}, nil

	case *codecs.MPEG4Video:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeMPEG4Video,
		}, nil

	case *codecs.MPEG1Video:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			// we use MPEG-2 to signal that video can be either MPEG-1 or MPEG-2
			StreamType: astits.StreamTypeMPEG2Video,
		}, nil

	// audio

	case *codecs.Opus:
		desc := c.Desc
		if desc == nil {
			desc = &substructs.OpusAudioDescriptor{
				ChannelConfigCode: uint8(c.ChannelCount), //nolint:staticcheck
			}
		}

		enc, _ := desc.Marshal()

		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypePrivateData,
			ElementaryStreamDescriptors: []*astits.Descriptor{
				{
					Length: 1,
					Tag:    astits.DescriptorTagRegistration,
					Registration: &astits.DescriptorRegistration{
						FormatIdentifier: opusIdentifier,
					},
				},
				{
					Length: 1,
					Tag:    astits.DescriptorTagExtension,
					Extension: &astits.DescriptorExtension{
						Tag:     0x80,
						Unknown: &enc,
					},
				},
			},
		}, nil

	case *codecs.AC3:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeAC3Audio,
			ElementaryStreamDescriptors: []*astits.Descriptor{
				{
					Length: 3,
					Tag:    astits.DescriptorTagAC3,
					AC3: &astits.DescriptorAC3{
						HasComponentType: true,
						ComponentType:    ac3ComponentType(c.ChannelCount, true),
						HasBSID:          true,
						BSID:             8,
					},
				},
			},
		}, nil

	case *codecs.EAC3:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeEAC3Audio,
			ElementaryStreamDescriptors: []*astits.Descriptor{
				{
					Length: 3,
					Tag:    astits.DescriptorTagEnhancedAC3,
					EnhancedAC3: &astits.DescriptorEnhancedAC3{
						HasComponentType: true,
						ComponentType:    eac3ComponentType(c.ChannelCount, true),
						HasBSID:          true,
						BSID:             16,
					},
				},
			},
		}, nil

	case *codecs.MPEG4Audio:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeAACAudio,
		}, nil

	case *codecs.MPEG4AudioLATM:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeAACLATMAudio,
		}, nil

	case *codecs.MPEG1Audio:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypeMPEG1Audio,
		}, nil

	// other

	case *codecs.KLV:
		if c.Synchronous {
			metadataDesc, err := substructs.MetadataDescriptor{
				MetadataApplicationFormat: metadataApplicationFormatGeneral,
				MetadataFormat:            0xFF,
				MetadataFormatIdentifier:  klvaIdentifier,
				MetadataServiceID:         0x00,
				DecoderConfigFlags:        0,
				DSMCCFlag:                 false,
			}.Marshal()
			if err != nil {
				return nil, err
			}

			metadataSTDDesc, err := substructs.MetadataSTDDescriptor{
				MetadataInputLeakRate:  0,
				MetadataBufferSize:     0,
				MetadataOutputLeakRate: 0,
			}.Marshal()
			if err != nil {
				return nil, err
			}

			return &astits.PMTElementaryStream{
				ElementaryPID: t.PID,
				StreamType:    astits.StreamTypeMetadata,
				ElementaryStreamDescriptors: []*astits.Descriptor{
					{
						Length: 1,
						Tag:    substructs.DescriptorTagMetadata,
						Unknown: &astits.DescriptorUnknown{
							Content: metadataDesc,
						},
					},
					{
						Length: 1,
						Tag:    substructs.DescriptorTagMetadataSTD,
						Unknown: &astits.DescriptorUnknown{
							Content: metadataSTDDesc,
						},
					},
				},
			}, nil
		}

		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypePrivateData,
			ElementaryStreamDescriptors: []*astits.Descriptor{
				{
					Length: 1,
					Tag:    astits.DescriptorTagRegistration,
					Registration: &astits.DescriptorRegistration{
						FormatIdentifier: klvaIdentifier,
					},
				},
			},
		}, nil

	case *codecs.DVBSubtitle:
		return &astits.PMTElementaryStream{
			ElementaryPID: t.PID,
			StreamType:    astits.StreamTypePrivateData,
			ElementaryStreamDescriptors: []*astits.Descriptor{
				{
					Length: 1,
					Tag:    astits.DescriptorTagSubtitling,
					Subtitling: &astits.DescriptorSubtitling{
						Items: c.Items,
					},
				},
			},
		}, nil

	default:
		panic("unsupported codec")
	}
}

func (t *Track) unmarshalFromES(dem *demuxer, es pmtElementaryStream) error {
	t.PID = es.pid

	codec, err := findCodecFromES(dem, es)
	if err != nil {
		return err
	}
	t.Codec = codec

	return nil
}
