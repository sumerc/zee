package transcriber

import (
	"encoding/binary"
	"fmt"
	"strings"
	"sync"
	"time"
	"zee/encoder"
)

type transcribeFunc func(audio []byte, format string) (*Result, error)

type batchSession struct {
	cfg        SessionConfig
	transcribe transcribeFunc
	encoder    encoder.Encoder
	updates    chan string
	blockChan  chan []int16
	encodeDone chan struct{}
	sampleBuf  []int16
	bufMu      sync.Mutex
}

func newBatchSession(cfg SessionConfig, transcribe transcribeFunc) (*batchSession, error) {
	enc, err := newEncoder(cfg.Format)
	if err != nil {
		return nil, err
	}

	bs := &batchSession{
		cfg:        cfg,
		transcribe: transcribe,
		encoder:    enc,
		updates:    make(chan string),
		blockChan:  make(chan []int16, 64),
		encodeDone: make(chan struct{}),
	}

	go func() {
		defer close(bs.encodeDone)
		for block := range bs.blockChan {
			start := time.Now()
			bs.encoder.EncodeBlock(block)
			bs.encoder.AddEncodeTime(time.Since(start))
		}
	}()

	return bs, nil
}

func (bs *batchSession) Feed(pcm []byte) {
	bs.bufMu.Lock()
	for i := 0; i+1 < len(pcm); i += 2 {
		bs.sampleBuf = append(bs.sampleBuf, int16(binary.LittleEndian.Uint16(pcm[i:])))
	}
	var blocks [][]int16
	for len(bs.sampleBuf) >= encoder.BlockSize {
		block := make([]int16, encoder.BlockSize)
		copy(block, bs.sampleBuf[:encoder.BlockSize])
		bs.sampleBuf = bs.sampleBuf[encoder.BlockSize:]
		blocks = append(blocks, block)
	}
	bs.bufMu.Unlock()

	for _, block := range blocks {
		bs.blockChan <- block
	}
}

func (bs *batchSession) Updates() <-chan string {
	return bs.updates
}

func (bs *batchSession) Close() (SessionResult, error) {
	// Flush remaining samples
	bs.bufMu.Lock()
	if len(bs.sampleBuf) > 0 {
		partial := make([]int16, len(bs.sampleBuf))
		copy(partial, bs.sampleBuf)
		bs.blockChan <- partial
	}
	bs.bufMu.Unlock()

	close(bs.blockChan)
	<-bs.encodeDone
	close(bs.updates)

	if err := bs.encoder.Close(); err != nil {
		return SessionResult{}, err
	}

	audioData := bs.encoder.Bytes()
	apiFormat := apiFormatFromConfig(bs.cfg.Format)

	result, err := bs.transcribe(audioData, apiFormat)
	if err != nil {
		return SessionResult{}, err
	}

	text := strings.TrimSpace(result.Text)
	noSpeech := text == ""

	enc := bs.encoder
	rawSize := enc.TotalFrames() * 2
	encodedSize := uint64(len(enc.Bytes()))
	compressionPct := (1.0 - float64(encodedSize)/float64(rawSize)) * 100
	audioDuration := float64(enc.TotalFrames()) / float64(encoder.SampleRate)
	netMetrics := result.Metrics

	sr := SessionResult{
		Text:      text,
		HasText:   !noSpeech,
		NoSpeech:  noSpeech,
		RateLimit: result.RateLimit,
		Batch: &BatchStats{
			AudioLengthS:     audioDuration,
			RawSizeKB:        float64(rawSize) / 1024,
			CompressedSizeKB: float64(encodedSize) / 1024,
			CompressionPct:   compressionPct,
			EncodeTimeMs:     float64(enc.EncodeTime().Milliseconds()),
			DNSTimeMs:        float64(netMetrics.DNS.Milliseconds()),
			TLSTimeMs:        float64(netMetrics.TLS.Milliseconds()),
			TTFBMs:           float64(netMetrics.TTFB.Milliseconds()),
			TotalTimeMs:      float64(netMetrics.Sum().Milliseconds()),
			ConnReused:       netMetrics.ConnReused,
			TLSProtocol:      netMetrics.TLSProtocol,
			Confidence:       result.Confidence,
		},
		Metrics: bs.formatMetrics(rawSize, encodedSize, compressionPct, audioDuration, result),
	}
	sr.captureMemStats()
	return sr, nil
}

func (bs *batchSession) formatMetrics(rawSize, encodedSize uint64, compressionPct, audioDuration float64, result *Result) []string {
	metrics := result.Metrics

	reusedStatus := ""
	if metrics.ConnReused {
		reusedStatus = " (reused)"
	}

	lines := []string{
		fmt.Sprintf("audio:      %.1fs | %.1f KB → %.1f KB (%.0f%% smaller)",
			audioDuration, float64(rawSize)/1024, float64(encodedSize)/1024, compressionPct),
		fmt.Sprintf("format:     %s", bs.cfg.Format),
		fmt.Sprintf("encode:     %dms (concurrent)", bs.encoder.EncodeTime().Milliseconds()),
		fmt.Sprintf("conn_wait:  %dms%s", metrics.ConnWait.Milliseconds(), reusedStatus),
		fmt.Sprintf("dns:        %dms", metrics.DNS.Milliseconds()),
		fmt.Sprintf("tcp:        %dms", metrics.TCP.Milliseconds()),
		fmt.Sprintf("tls:        %dms", metrics.TLS.Milliseconds()),
		fmt.Sprintf("req_head:   %dms", metrics.ReqHeaders.Milliseconds()),
		fmt.Sprintf("req_body:   %dms", metrics.ReqBody.Milliseconds()),
		fmt.Sprintf("ttfb:       %dms", metrics.TTFB.Milliseconds()),
		fmt.Sprintf("download:   %dms", metrics.Download.Milliseconds()),
		fmt.Sprintf("total:      %dms", metrics.Sum().Milliseconds()),
	}
	if result.Duration > 0 {
		lines = append(lines, fmt.Sprintf("api_dur:    %.2fs", result.Duration))
	}
	if result.Confidence > 0 {
		lines = append(lines, fmt.Sprintf("confidence: %.4f", result.Confidence))
	}

	return lines
}

func newEncoder(format string) (encoder.Encoder, error) {
	switch format {
	case "mp3@16":
		return encoder.NewMp3(16)
	case "mp3@64":
		return encoder.NewMp3(64)
	case "flac":
		return encoder.NewFlac()
	default:
		return nil, fmt.Errorf("unknown format %q", format)
	}
}

func apiFormatFromConfig(format string) string {
	switch format {
	case "flac":
		return "flac"
	default:
		return "mp3"
	}
}
