package mpegts

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/substructs"
)

type writeCounter struct {
	calls int
	bytes int
}

func (wc *writeCounter) Write(p []byte) (int, error) {
	wc.calls++
	wc.bytes += len(p)
	return len(p), nil
}

func (wc *writeCounter) reset() {
	wc.calls = 0
	wc.bytes = 0
}

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

func TestWriteCallCounts(t *testing.T) {
	makePayload := func(size int) []byte {
		p := make([]byte, size)
		for i := range p {
			p[i] = byte(i)
		}
		return p
	}

	cases := []struct {
		name   string
		frames int
		write  func(w *Writer, track *Track, i int) error
		codec  codecs.Codec
	}{
		{
			name:   "h264_idr_100KB",
			frames: 1,
			codec:  &codecs.H264{},
			write: func(w *Writer, track *Track, i int) error {
				idrPayload := makePayload(100_000)
				au := [][]byte{testH264SPS, {8}, idrPayload}
				return w.WriteH264(track, int64(i)*3600, int64(i)*3600, au)
			},
		},
		{
			name:   "h264_non_idr_20KB",
			frames: 10,
			codec:  &codecs.H264{},
			write: func(w *Writer, track *Track, i int) error {
				payload := makePayload(20_000)
				au := [][]byte{payload}
				return w.WriteH264(track, int64(i+1)*3600+3600, int64(i+1)*3600, au)
			},
		},
		{
			name:   "opus_50B",
			frames: 10,
			codec: &codecs.Opus{
				Desc:         &substructs.OpusAudioDescriptor{ChannelConfigCode: 2},
				ChannelCount: 2,
			},
			write: func(w *Writer, track *Track, i int) error {
				packets := [][]byte{makePayload(50)}
				return w.WriteOpus(track, int64(i)*960, packets)
			},
		},
		{
			name:   "mpeg4_audio_200B",
			frames: 10,
			codec: &codecs.MPEG4Audio{
				Config: mpeg4audio.AudioSpecificConfig{
					Type: 2, SampleRate: 48000, ChannelConfig: 2, ChannelCount: 2,
				},
			},
			write: func(w *Writer, track *Track, i int) error {
				aus := [][]byte{makePayload(200)}
				return w.WriteMPEG4Audio(track, int64(i)*1920, aus)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc := &writeCounter{}
			track := &Track{PID: 256, Codec: tc.codec}
			w := NewWriter(wc, []*Track{track})

			// warm up: write an IDR first if testing non-IDR
			if tc.name == "h264_non_idr_20KB" {
				au := [][]byte{testH264SPS, {8}, {5}}
				_ = w.WriteH264(track, 0, 0, au)
			}

			totalCalls := 0
			totalBytes := 0
			for i := range tc.frames {
				wc.reset()
				err := tc.write(w, track, i)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Printf("  frame %d: %d Write() calls, %d bytes\n", i, wc.calls, wc.bytes)
				totalCalls += wc.calls
				totalBytes += wc.bytes
			}
			avgCalls := float64(totalCalls) / float64(tc.frames)
			avgBytes := float64(totalBytes) / float64(tc.frames)
			fmt.Printf("  AVERAGE: %.1f Write() calls, %.0f bytes per frame\n\n", avgCalls, avgBytes)

			// now test with bufio.Writer
			wc.reset()
			bw := bufio.NewWriterSize(wc, 7*188)
			track2 := &Track{PID: 256, Codec: tc.codec}
			w2 := NewWriter(bw, []*Track{track2})

			if tc.name == "h264_non_idr_20KB" {
				au := [][]byte{testH264SPS, {8}, {5}}
				_ = w2.WriteH264(track2, 0, 0, au)
			}

			totalCallsBuf := 0
			totalBytesBuf := 0
			for i := range tc.frames {
				wc.reset()
				err := tc.write(w2, track2, i)
				if err != nil {
					t.Fatal(err)
				}
				_ = bw.Flush()
				fmt.Printf("  frame %d (buffered): %d Write() calls, %d bytes\n", i, wc.calls, wc.bytes)
				totalCallsBuf += wc.calls
				totalBytesBuf += wc.bytes
			}
			avgCallsBuf := float64(totalCallsBuf) / float64(tc.frames)
			fmt.Printf("  AVERAGE (buffered): %.1f Write() calls per frame\n", avgCallsBuf)
			fmt.Printf("  REDUCTION: %.1fx fewer Write() calls\n\n", avgCalls/avgCallsBuf)
		})
	}
}
