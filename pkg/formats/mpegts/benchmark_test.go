package mpegts

import (
	"bytes"
	"io"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/substructs"
)

const benchFrameCount = 100

func generateTSStream(track *Track, samples []sample) []byte {
	var buf bytes.Buffer
	trackCopy := &Track{PID: track.PID, Codec: track.Codec}
	w := NewWriter(&buf, []*Track{trackCopy})

	for j := range benchFrameCount {
		for _, s := range samples {
			pts := s.pts + int64(j)*90000
			dts := s.dts + int64(j)*90000

			switch trackCopy.Codec.(type) {
			case *codecs.H265:
				_ = w.WriteH265(trackCopy, pts, dts, s.data)
			case *codecs.H264:
				_ = w.WriteH264(trackCopy, pts, dts, s.data)
			case *codecs.MPEG4Video:
				_ = w.WriteMPEG4Video(trackCopy, pts, s.data[0])
			case *codecs.MPEG1Video:
				_ = w.WriteMPEG1Video(trackCopy, pts, s.data[0])
			case *codecs.Opus:
				_ = w.WriteOpus(trackCopy, pts, s.data)
			case *codecs.MPEG4Audio:
				_ = w.WriteMPEG4Audio(trackCopy, pts, s.data)
			case *codecs.MPEG4AudioLATM:
				_ = w.WriteMPEG4AudioLATM(trackCopy, pts, s.data)
			case *codecs.MPEG1Audio:
				_ = w.WriteMPEG1Audio(trackCopy, pts, s.data)
			case *codecs.AC3:
				_ = w.WriteAC3(trackCopy, pts, s.data[0])
			case *codecs.EAC3:
				_ = w.WriteEAC3(trackCopy, pts, s.data[0])
			case *codecs.KLV:
				_ = w.WriteKLV(trackCopy, pts, s.data[0])
			case *codecs.DVBSubtitle:
				_ = w.WriteDVBSubtitle(trackCopy, pts, s.data[0])
			}
		}
	}

	return buf.Bytes()
}

func setupReaderCallback(r *Reader, track *Track) {
	switch track.Codec.(type) {
	case *codecs.H265:
		r.OnDataH265(r.Tracks()[0], func(_, _ int64, _ [][]byte) error { return nil })
	case *codecs.H264:
		r.OnDataH264(r.Tracks()[0], func(_, _ int64, _ [][]byte) error { return nil })
	case *codecs.MPEG4Video, *codecs.MPEG1Video:
		r.OnDataMPEGxVideo(r.Tracks()[0], func(_ int64, _ []byte) error { return nil })
	case *codecs.Opus:
		r.OnDataOpus(r.Tracks()[0], func(_ int64, _ [][]byte) error { return nil })
	case *codecs.MPEG4Audio:
		r.OnDataMPEG4Audio(r.Tracks()[0], func(_ int64, _ [][]byte) error { return nil })
	case *codecs.MPEG4AudioLATM:
		r.OnDataMPEG4AudioLATM(r.Tracks()[0], func(_ int64, _ [][]byte) error { return nil })
	case *codecs.MPEG1Audio:
		r.OnDataMPEG1Audio(r.Tracks()[0], func(_ int64, _ [][]byte) error { return nil })
	case *codecs.AC3:
		r.OnDataAC3(r.Tracks()[0], func(_ int64, _ []byte) error { return nil })
	case *codecs.EAC3:
		r.OnDataEAC3(r.Tracks()[0], func(_ int64, _ []byte) error { return nil })
	case *codecs.KLV:
		r.OnDataKLV(r.Tracks()[0], func(_ int64, _ []byte) error { return nil })
	case *codecs.DVBSubtitle:
		r.OnDataDVBSubtitle(r.Tracks()[0], func(_ int64, _ []byte) error { return nil })
	}
}

func BenchmarkReader(b *testing.B) {
	for _, ca := range casesReadWriter {
		b.Run(ca.name, func(b *testing.B) {
			tsData := generateTSStream(ca.track, ca.samples)

			b.ResetTimer()
			b.ReportAllocs()

			for range b.N {
				r := &Reader{R: bytes.NewReader(tsData)}
				if err := r.Initialize(); err != nil {
					b.Fatal(err)
				}
				setupReaderCallback(r, ca.track)

				for {
					if err := r.Read(); err != nil {
						break
					}
				}
			}
		})
	}
}

func BenchmarkWriter(b *testing.B) {
	b.Run("h265", func(b *testing.B) {
		track := &Track{PID: 257, Codec: &codecs.H265{}}
		w := NewWriter(io.Discard, []*Track{track})
		au := [][]byte{
			testH265SPS,
			testH265PPS,
			{byte(h265.NALUType_CRA_NUT) << 1},
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i) * 3600
			_ = w.WriteH265(track, pts, pts, au)
		}
	})

	b.Run("h264", func(b *testing.B) {
		track := &Track{PID: 256, Codec: &codecs.H264{}}
		w := NewWriter(io.Discard, []*Track{track})
		au := [][]byte{testH264SPS, {8}, {5}}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i) * 3600
			_ = w.WriteH264(track, pts, pts, au)
		}
	})

	b.Run("h264_non_idr", func(b *testing.B) {
		track := &Track{PID: 256, Codec: &codecs.H264{}}
		w := NewWriter(io.Discard, []*Track{track})
		_ = w.WriteH264(track, 0, 0, [][]byte{testH264SPS, {8}, {5}})
		au := [][]byte{{1}}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i+1) * 3600
			_ = w.WriteH264(track, pts+3600, pts, au)
		}
	})

	b.Run("opus", func(b *testing.B) {
		track := &Track{PID: 257, Codec: &codecs.Opus{
			Desc:         &substructs.OpusAudioDescriptor{ChannelConfigCode: 2},
			ChannelCount: 2,
		}}
		w := NewWriter(io.Discard, []*Track{track})
		packets := [][]byte{{3, 4, 5}, {6, 7, 8}}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i) * 960
			_ = w.WriteOpus(track, pts, packets)
		}
	})

	b.Run("mpeg4_audio", func(b *testing.B) {
		track := &Track{PID: 257, Codec: &codecs.MPEG4Audio{
			Config: mpeg4audio.AudioSpecificConfig{
				Type:          2,
				SampleRate:    48000,
				ChannelConfig: 2,
				ChannelCount:  2,
			},
		}}
		w := NewWriter(io.Discard, []*Track{track})
		aus := [][]byte{{3, 4, 5}, {6, 7, 8}}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i) * 1920
			_ = w.WriteMPEG4Audio(track, pts, aus)
		}
	})

	b.Run("mpeg1_audio", func(b *testing.B) {
		track := &Track{PID: 257, Codec: &codecs.MPEG1Audio{}}
		w := NewWriter(io.Discard, []*Track{track})
		frames := [][]byte{casesReadWriter[7].samples[0].data[0]} // mpeg-1 audio frame from test data

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			pts := int64(i) * 1152
			_ = w.WriteMPEG1Audio(track, pts, frames)
		}
	})
}
