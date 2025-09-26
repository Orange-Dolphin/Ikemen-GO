package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"github.com/gopxl/beep/v2"
	"github.com/ikemen-engine/reisen"
)

type bgVideo struct {
	started     bool
	errs        chan error
	frameBuffer chan *image.RGBA
	audioBuffer chan []float64 // interleaved L,R float64 samples
	audioStream *reisen.AudioStream
	videoStream *reisen.VideoStream
	media       *reisen.Media
	loop        bool
	texture     Texture
	startWall   time.Time
	basePTS     time.Duration
	haveBasePTS bool
	elapsedPTS  time.Duration
	lastFrame   *image.RGBA
	volume      int
	scale       BgVideoScale
	flag        BgVideoFlag
	playing     bool
	visible     bool
}

const (
	frameBufferSize = 10
)

// reisenAudioStreamer adapts chunks of interleaved float64 samples from a channel
// into a Beep Streamer. It never blocks the audio callback: when starved, it emits
// silence to keep clocking; when the channel is closed and pending is drained, it ends.
type reisenAudioStreamer struct {
	ch      <-chan []float64
	pending []float64
	closed  bool
}

func (rs *reisenAudioStreamer) Err() error {
	return nil
}

func (rs *reisenAudioStreamer) Stream(out [][2]float64) (n int, ok bool) {
	// Fill out as much as we can. If we run out of pending data and the channel
	// is closed, we end; otherwise we output silence for the remainder to avoid XRuns.
	for i := 0; i < len(out); i++ {
		if len(rs.pending) < 2 {
			if !rs.closed {
				select {
				case chunk, okc := <-rs.ch:
					if okc {
						rs.pending = append(rs.pending, chunk...)
					} else {
						rs.closed = true
					}
				default:
					// No data right now. Emit silence this sample to keep the callback brief.
					out[i][0], out[i][1] = 0, 0
					n++
					continue
				}
			}
			if rs.closed && len(rs.pending) < 2 {
				// No more data and nothing pending: end of stream.
				return n, n > 0
			}
		}
		out[i][0] = rs.pending[0]
		out[i][1] = rs.pending[1]
		rs.pending = rs.pending[2:]
		n++
	}
	return n, true
}

func (bgv *bgVideo) Open(filename string, volume int, sc BgVideoScale, sf BgVideoFlag, loop bool) error {
	//fmt.Println("Opening media file:", filename)
	m, err := reisen.NewMedia(filename)
	if err != nil {
		return err
	}

	//bgv.describe()

	bgv.volume = volume
	bgv.scale = sc
	bgv.flag = sf
	bgv.loop = loop
	bgv.media = m
	bgv.playing = false
	bgv.visible = true
	bgv.elapsedPTS = 0
	bgv.haveBasePTS = false

	bgv.frameBuffer = make(chan *image.RGBA, frameBufferSize)
	bgv.audioBuffer = make(chan []float64, 128)
	bgv.errs = make(chan error)

	err = bgv.media.OpenDecode()
	if err != nil {
		return err
	}

	videoStreams := bgv.media.VideoStreams()
	if len(videoStreams) == 0 {
		return fmt.Errorf("No decodable video streams in %s (check codecs in your FFmpeg build)", filename)
	}

	err = videoStreams[0].Open()
	if err != nil {
		return err
	}

	// Configure FFmpeg scaling/padding via AVFilter (scale+pad) for video.
	// We compute the desired target based on window size and policy.
	if v := videoStreams[0]; v != nil {
		srcW, srcH := v.Width(), v.Height()
		winW, winH := int(sys.scrrect[2]), int(sys.scrrect[3])

		if fg := buildFFFilterGraph(srcW, srcH, winW, winH, sc, sf); fg != "" {
			if err := v.ApplyVideoFilterGraph(fg); err != nil {
				// Don't fail playback if filter graph can't be applied; fall back to sws_scale path.
				sys.errLog.Printf("video: ApplyVideoFilterGraph failed (%v) for graph '%s', using sws_scale fallback", err, fg)
			}
		}
	}
	bgv.videoStream = videoStreams[0]

	// Try to open the first audio stream, if any
	audioStreams := bgv.media.AudioStreams()
	if len(audioStreams) > 0 {
		if err := audioStreams[0].Open(); err == nil {
			bgv.audioStream = audioStreams[0]
			// Hand off a streamer on the same channel path as regular BGM.
			rs := &reisenAudioStreamer{ch: bgv.audioBuffer}
			sys.bgm.OpenFromStreamer(rs, beep.SampleRate(audioStreams[0].SampleRate()), volume)
		} else {
			return fmt.Errorf("Audio stream open failed: %v", err)
		}
	}

	// Normalize timeline
	if err := videoStreams[0].Rewind(0); err != nil {
		return fmt.Errorf("Rewind(0) failed: %v", err)
	}

	bgv.haveBasePTS = false

	// Decode loop. When EOF is reached and loop==true, rewind to t=0 and continue.
	go func() {
		for {
			// Do not demux/decode while paused: that keeps A/V frozen and avoids audio blips.
			if !bgv.playing {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			gotPacket := bgv.processPacket()
			if gotPacket {
				continue
			}
			// No packet => demuxer exhausted or error already reported via errs.
			// If looping is requested, rewind to the beginning and reset pacing.
			if bgv.loop {
				// Prefer rewinding the VIDEO stream to keep A/V in sync per reisen docs.
				if err := bgv.videoStream.Rewind(0); err != nil {
					bgv.errs <- fmt.Errorf("loop rewind video failed: %v", err)
					break
				}
				// Optional: also rewind audio if present; safe no-op if demux-only.
				if bgv.audioStream != nil {
					_ = bgv.audioStream.Rewind(0)
				}
				// Reset pacing baseline so PresentationOffset() maps to fresh wall clock.
				bgv.haveBasePTS = false
				// Keep elapsedPTS at 0; resume will re-anchor startWall.
				bgv.elapsedPTS = 0
				// Continue producing frames/samples on the same channels.
				continue
			}
			// Not looping -> finish and clean up.
			break
		}
		// Cleanup only when we actually finish (i.e., not looping forever).
		bgv.videoStream.Close()
		if bgv.audioStream != nil {
			bgv.audioStream.Close()
		}
		if bgv.media != nil {
			bgv.media.CloseDecode()
		}
		close(bgv.frameBuffer)
		close(bgv.audioBuffer)
		close(bgv.errs)
	}()

	return nil
}

func (bgv *bgVideo) describe() error {
	// Print the media properties.
	dur, err := bgv.media.Duration()
	if err != nil {
		return err
	}
	fmt.Println("Duration:", dur)
	fmt.Println("Format name:", bgv.media.FormatName())
	fmt.Println("Format long name:", bgv.media.FormatLongName())
	fmt.Println("MIME type:", bgv.media.FormatMIMEType())
	fmt.Println("Number of streams:", bgv.media.StreamCount())
	fmt.Println()

	// Enumerate the media file streams.
	for _, stream := range bgv.media.Streams() {
		dur, err := stream.Duration()
		if err != nil {
			return err
		}
		tbNum, tbDen := stream.TimeBase()
		fpsNum, fpsDen := stream.FrameRate()
		fmt.Println("Index:", stream.Index())
		fmt.Println("Stream type:", stream.Type())
		fmt.Println("Codec name:", stream.CodecName())
		fmt.Println("Codec long name:", stream.CodecLongName())
		fmt.Println("Stream duration:", dur)
		fmt.Println("Stream bit rate:", stream.BitRate())
		fmt.Printf("Time base: %d/%d\n", tbNum, tbDen)
		fmt.Printf("Frame rate: %d/%d\n", fpsNum, fpsDen)
		fmt.Println("Frame count:", stream.FrameCount())
		fmt.Println()
	}
	return nil
}

func (bgv *bgVideo) processPacket() bool {
	if bgv.media == nil {
		// No media yet; nothing to do.
		return false
	}
	packet, gotPacket, err := bgv.media.ReadPacket()
	if err != nil {
		bgv.errs <- err
	}

	if !gotPacket {
		return false
	}

	switch packet.Type() {
	case reisen.StreamVideo:
		s := bgv.media.Streams()[packet.StreamIndex()].(*reisen.VideoStream)
		vf, gotFrame, err := s.ReadVideoFrame()

		if err != nil {
			bgv.errs <- err
		}

		// Keep decoding even if this packet didn't yield a frame.
		if gotFrame && vf != nil {
			// Pace on producer side: sleep until the frame's presentation time.
			if off, err := vf.PresentationOffset(); err == nil {
				// Rebase to first frame
				if !bgv.haveBasePTS {
					bgv.basePTS = off
					bgv.haveBasePTS = true
				}
				elapsed := off - bgv.basePTS
				if elapsed < 0 {
					elapsed = 0
				}
				bgv.elapsedPTS = elapsed
				sleepUntil := bgv.startWall.Add(elapsed)
				d := time.Until(sleepUntil)
				if d > 0 {
					time.Sleep(d)
				}
			}
			// Deliver only when visible; while hidden we still pace timers but drop frames.
			if bgv.visible {
				bgv.frameBuffer <- vf.Image()
				bgv.lastFrame = vf.Image() // remember last for sticky reupload
			}
		}

	case reisen.StreamAudio:
		// Only push audio while playing; otherwise streamer emits silence.
		if !bgv.playing {
			return true
		}
		// Decode to float64 interleaved samples and push to audioBuffer.
		// Reisen delivers stereo float64 as little-endian bytes (L,R,L,R,...) per frame.
		// We do NOT sleep here; the Beep speaker drives timing and back-pressures via the channel.
		s := bgv.media.Streams()[packet.StreamIndex()].(*reisen.AudioStream)
		af, gotFrame, err := s.ReadAudioFrame()
		if err != nil {
			bgv.errs <- err
		}
		if gotFrame && af != nil {
			raw := af.Data()
			if len(raw) >= 16 { // at least one stereo sample (2 * float64)
				count := len(raw) / 8
				samples := make([]float64, 0, count)
				for i := 0; i+8 <= len(raw); i += 8 {
					u := binary.LittleEndian.Uint64(raw[i : i+8])
					samples = append(samples, math.Float64frombits(u))
				}
				if bgv.visible {
					// Never block the decode loop on audio delivery.
					// If speaker can't keep up, drop this chunk to keep video moving.
					select {
					case bgv.audioBuffer <- samples:
					default:
						// drop
					}
				} else {
					// Hidden: drop audio to remain silent while timers continue to run.
				}
			}
		}
	}

	return true
}

func (bgv *bgVideo) Tick() error {
	// fmt.Println("Tick video... ", bgv, bgv.started)
	select {
	case err, ok := <-bgv.errs:
		if ok {
			return err
		}

	default:
	}

	if !bgv.started {
		bgv.started = true
	}

	// Non-blocking receive so render loop never stalls
	select {
	case frame, ok := <-bgv.frameBuffer:
		if ok {
			// Upload the (possibly FFmpeg-scaled/padded) frame as-is.
			rect := frame.Bounds()
			w := int32(rect.Dx())
			h := int32(rect.Dy())
			if bgv.texture == nil || w != bgv.texture.GetWidth() || h != bgv.texture.GetHeight() {
				bgv.texture = gfx.newTexture(w, h, 32, true)
			}
			bgv.texture.SetData(frame.Pix)
			bgv.lastFrame = frame
		}
	default:
		// No new frame right now. Re-upload last to keep video visible.
		if bgv.lastFrame != nil {
			rect := bgv.lastFrame.Bounds()
			w := int32(rect.Dx())
			h := int32(rect.Dy())
			if bgv.texture == nil || w != bgv.texture.GetWidth() || h != bgv.texture.GetHeight() {
				bgv.texture = gfx.newTexture(w, h, 32, true)
			}
			bgv.texture.SetData(bgv.lastFrame.Pix)
		}
	}

	// fmt.Println("Ticked video... ", bgv, bgv.started)
	return nil
}

// SetPlaying toggles decode/audio production and (re)anchors the pacing clock.
func (bgv *bgVideo) SetPlaying(on bool) {
	if on == bgv.playing {
		return
	}
	if on {
		// If we have not established a baseline PTS in this decode epoch,
		// rewind to t=0 so the first decoded frame is a keyframe.
		if !bgv.haveBasePTS && bgv.videoStream != nil {
			_ = bgv.videoStream.Rewind(0)
		}
		// Anchor wall clock so that current elapsedPTS displays "now".
		bgv.startWall = time.Now().Add(-bgv.elapsedPTS)
		bgv.playing = true
	} else {
		// Freeze: decode loop will idle until re-enabled.
		bgv.playing = false
	}
}

// SetVisible controls whether decoded A/V is presented.
// When turning invisible, drain any queued audio so the mixer goes silent immediately.
func (bgv *bgVideo) SetVisible(on bool) {
	if on == bgv.visible {
		return
	}
	bgv.visible = on
	if !on {
		// Drop any queued audio so the Beep streamer outputs silence right away.
		for {
			select {
			case <-bgv.audioBuffer:
				// keep draining
			default:
				return
			}
		}
	}
}

// Reset rewinds streams to t=0 and clears pacing; playback remains paused.
func (bgv *bgVideo) Reset() {
	if bgv.videoStream != nil {
		_ = bgv.videoStream.Rewind(0)
	}
	if bgv.audioStream != nil {
		_ = bgv.audioStream.Rewind(0)
	}
	bgv.haveBasePTS = false
	bgv.elapsedPTS = 0
	bgv.lastFrame = nil
}

// buildFFFilterGraph builds a scale(+optional crop/pad)+format filtergraph string for FFmpeg.
func buildFFFilterGraph(sw, sh, ww, wh int, sc BgVideoScale, sf BgVideoFlag) string {
	if ww <= 0 || wh <= 0 || sw <= 0 || sh <= 0 {
		return ""
	}

	// Map our filter enum to FFmpeg scale flags.
	flag := "bicubic"
	switch sf {
	case SF_FastBilinear:
		flag = "fast_bilinear"
	case SF_Bilinear:
		flag = "bilinear"
	case SF_Bicubic:
		flag = "bicubic"
	case SF_Experimental:
		flag = "experimental"
	case SF_Neighbor:
		flag = "neighbor"
	case SF_Area:
		flag = "area"
	case SF_Bicublin:
		flag = "bicublin"
	case SF_Gauss:
		flag = "gauss"
	case SF_Sinc:
		flag = "sinc"
	case SF_Lanczos:
		flag = "lanczos"
	case SF_Spline:
		flag = "spline"
	}

	// Helpers
	scaleExact := func(w, h int) string {
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		return fmt.Sprintf("scale=%d:%d:flags=%s", w, h, flag)
	}
	// Ask FFmpeg to keep AR and compute the other dimension; -2 keeps it even when needed.
	scaleToW := func(w int) string {
		if w < 1 {
			w = 1
		}
		return fmt.Sprintf("scale=%d:-2:flags=%s:force_divisible_by=2", w, flag)
	}
	scaleToH := func(h int) string {
		if h < 1 {
			h = 1
		}
		return fmt.Sprintf("scale=-2:%d:flags=%s:force_divisible_by=2", h, flag)
	}
	padCenter := func(w, h int) string {
		return fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", w, h)
	}
	// Center-crop vertically to a maximum height; no-op if ih<=h.
	cropCenterH := func(w, h int) string {
		// After width-constrained scale, iw≈w; keep width w, clamp height to h.
		return fmt.Sprintf("crop=%d:min(ih\\,%d):0:floor((ih-min(ih\\,%d))/2)", w, h, h)
	}
	// Center-crop horizontally to a maximum width; no-op if iw<=w.
	cropCenterW := func(w, h int) string {
		// After height-constrained scale, ih≈h; keep height h, clamp width to w.
		return fmt.Sprintf("crop=min(iw\\,%d):%d:floor((iw-min(iw\\,%d))/2):0", w, h, w)
	}

	var parts []string
	switch sc {
	case SC_None:
		// None: draw at native resolution, no scaling or padding.
		return ""

	case SC_Stretch:
		// Stretch: fill window exactly, distorting aspect ratio (no bars, no crop).
		parts = append(parts, scaleExact(ww, wh), "format=rgba")

	case SC_Fit:
		// Fit (contain): uniform scale so entire video fits inside window; add bars if needed.
		parts = append(parts,
			fmt.Sprintf("scale=%d:%d:flags=%s:force_original_aspect_ratio=decrease:force_divisible_by=2", ww, wh, flag),
			padCenter(ww, wh),
			"format=rgba",
		)

	case SC_FitWidth:
		// FitWidth: match window width; keep AR.
		// If height exceeds window -> center-crop vertically; if smaller -> pad vertically.
		parts = append(parts,
			scaleToW(ww),
			cropCenterH(ww, wh), // crops when ih>wh; no-op when ih<=wh
			padCenter(ww, wh),   // pads when ih<wh; no-op when ih>=wh
			"format=rgba",
		)

	case SC_FitHeight:
		// FitHeight: match window height; keep AR.
		// If width exceeds window -> center-crop horizontally; if smaller -> pad horizontally.
		parts = append(parts,
			scaleToH(wh),
			cropCenterW(ww, wh), // crops when iw>ww; no-op when iw<=ww
			padCenter(ww, wh),   // pads when iw<ww; no-op when iw>=ww
			"format=rgba",
		)

	case SC_ZoomFill:
		// ZoomFill (cover): uniform scale until content covers the window; center-crop overflow.
		parts = append(parts,
			fmt.Sprintf("scale=%d:%d:flags=%s:force_original_aspect_ratio=increase:force_divisible_by=2", ww, wh, flag),
			// Now both iw>=ww and ih>=wh, so crop center to the window.
			fmt.Sprintf("crop=%d:%d:floor((iw-%d)/2):floor((ih-%d)/2)", ww, wh, ww, wh),
			"format=rgba",
		)

	case SC_Center:
		// Center (no scale): center the native frame; crop if larger, pad if smaller.
		parts = append(parts,
			// First trim to window bounds (no-op if already smaller).
			fmt.Sprintf("crop=min(iw\\,%d):min(ih\\,%d):floor((iw-min(iw\\,%d))/2):floor((ih-min(ih\\,%d))/2)", ww, wh, ww, wh),
			// Then pad out to the window (no-op if already exact).
			padCenter(ww, wh),
			"format=rgba",
		)
	}

	out := strings.Join(parts, ",")
	//fmt.Println(out)
	return out
}
