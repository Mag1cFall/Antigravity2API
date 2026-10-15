package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"strings"

	"github.com/mewkiz/flac"
	"github.com/oov/audio/resampler"
	mp3 "github.com/tphakala/go-mp3/pcm"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

const (
	// transcriptionRate 为语音转写模型接收的 PCM 采样率
	transcriptionRate = 16000
	// resamplerQuality 为 Speex 重采样器的最高质量档
	resamplerQuality = 10
)

// transcriptionPCM 把 WAV、MP3、FLAC 或带 rate 参数的 audio/l16 解码为 16 kHz 单声道 16 位小端 PCM，多声道取平均
func transcriptionPCM(blob antigravity.Blob) ([]byte, error) {
	baseType, parameters, _ := mime.ParseMediaType(blob.MIME)
	var samples []float64
	var rate int
	var err error
	switch {
	case bytes.HasPrefix(blob.Data, []byte("RIFF")) && len(blob.Data) >= 12 && string(blob.Data[8:12]) == "WAVE":
		samples, rate, err = wavSamples(blob.Data)
	case bytes.HasPrefix(blob.Data, []byte("fLaC")):
		samples, rate, err = flacSamples(blob.Data)
	case baseType == "audio/mpeg" || baseType == "audio/mp3" || bytes.HasPrefix(blob.Data, []byte("ID3")):
		samples, rate, err = mp3Samples(blob.Data)
	case baseType == "audio/l16" || baseType == "audio/pcm":
		if _, err = fmt.Sscan(parameters["rate"], &rate); err != nil || rate <= 0 {
			return nil, fmt.Errorf("%s requires a rate parameter", blob.MIME)
		}
		channels := 1
		fmt.Sscan(parameters["channels"], &channels)
		samples = intSamples(blob.Data, 2, max(channels, 1), func(data []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(data))) / 32768 })
	default:
		return nil, fmt.Errorf("audio %s cannot be decoded for the transcription model: send WAV, MP3, FLAC or audio/l16 PCM, or use a Gemini model for this audio format", blob.MIME)
	}
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", blob.MIME, err)
	}
	if len(samples) == 0 {
		return nil, errors.New("audio contains no samples")
	}
	if rate != transcriptionRate {
		converter := resampler.NewWithSkipZeros(1, rate, transcriptionRate, resamplerQuality)
		input := append(samples, make([]float64, converter.InputLatency())...)
		output := make([]float64, (len(samples)*transcriptionRate+rate-1)/rate)
		_, written := converter.ProcessFloat64(0, input, output)
		samples = output[:written]
	}
	pcm := make([]byte, 2*len(samples))
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(pcm[2*index:], uint16(int16(math.Round(max(-32768, min(32767, sample*32768))))))
	}
	return pcm, nil
}

// intSamples 把交错的定点或浮点采样按声道平均为单声道，decode 把一个采样换成 [-1, 1] 的值
func intSamples(data []byte, width, channels int, decode func([]byte) float64) []float64 {
	frames := len(data) / (width * channels)
	samples := make([]float64, frames)
	for frame := range frames {
		var sum float64
		for channel := range channels {
			offset := (frame*channels + channel) * width
			sum += decode(data[offset : offset+width])
		}
		samples[frame] = sum / float64(channels)
	}
	return samples
}

// wavSamples 解析 RIFF WAVE 的 PCM 8/16/24/32 位整数或 32 位浮点数据
func wavSamples(data []byte) ([]float64, int, error) {
	var format, channels, bits uint16
	var rate uint32
	var pcm []byte
	for offset := 12; offset+8 <= len(data); {
		kind, size := string(data[offset:offset+4]), int(binary.LittleEndian.Uint32(data[offset+4:offset+8]))
		start := offset + 8
		end := min(start+size, len(data))
		chunk := data[start:end]
		switch kind {
		case "fmt ":
			if len(chunk) < 16 {
				return nil, 0, errors.New("WAV fmt chunk is too short")
			}
			format, channels = binary.LittleEndian.Uint16(chunk[0:2]), binary.LittleEndian.Uint16(chunk[2:4])
			rate, bits = binary.LittleEndian.Uint32(chunk[4:8]), binary.LittleEndian.Uint16(chunk[14:16])
			if format == 0xfffe && len(chunk) >= 26 {
				format = binary.LittleEndian.Uint16(chunk[24:26])
			}
		case "data":
			pcm = chunk
		}
		offset = start + size + size%2
	}
	if rate == 0 || channels == 0 || pcm == nil {
		return nil, 0, errors.New("WAV is missing fmt or data")
	}
	width := int(bits) / 8
	var decode func([]byte) float64
	switch {
	case format == 1 && bits == 8:
		decode = func(b []byte) float64 { return (float64(b[0]) - 128) / 128 }
	case format == 1 && bits == 16:
		decode = func(b []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(b))) / 32768 }
	case format == 1 && bits == 24:
		decode = func(b []byte) float64 {
			return float64(int32(uint32(b[0])<<8|uint32(b[1])<<16|uint32(b[2])<<24)>>8) / 8388608
		}
	case format == 1 && bits == 32:
		decode = func(b []byte) float64 { return float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648 }
	case format == 3 && bits == 32:
		decode = func(b []byte) float64 { return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) }
	default:
		return nil, 0, fmt.Errorf("WAV format %d with %d bits is not PCM", format, bits)
	}
	return intSamples(pcm, width, int(channels), decode), int(rate), nil
}

// mp3Samples 解码 MPEG 音频
func mp3Samples(data []byte) ([]float64, int, error) {
	pcm, info, err := mp3.DecodeInterleaved(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	return intSamples(pcm, 2, max(info.Channels, 1), func(b []byte) float64 { return float64(int16(binary.LittleEndian.Uint16(b))) / 32768 }), info.SampleRate, nil
}

// flacSamples 解码 FLAC
func flacSamples(data []byte) ([]float64, int, error) {
	stream, err := flac.New(bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	defer stream.Close()
	scale := math.Ldexp(1, int(stream.Info.BitsPerSample)-1)
	var samples []float64
	for {
		frame, err := stream.ParseNext()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, 0, err
		}
		for index := range frame.Subframes[0].Samples {
			var sum float64
			for _, subframe := range frame.Subframes {
				sum += float64(subframe.Samples[index])
			}
			samples = append(samples, sum/float64(len(frame.Subframes))/scale)
		}
	}
	return samples, int(stream.Info.SampleRate), nil
}

// audioParts 返回消息中全部音频内联媒体，按出现顺序
func audioParts(contents []antigravity.Content) []antigravity.Blob {
	var blobs []antigravity.Blob
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.InlineData != nil && strings.HasPrefix(strings.ToLower(part.InlineData.MIME), "audio/") {
				blobs = append(blobs, *part.InlineData)
			}
		}
	}
	return blobs
}
