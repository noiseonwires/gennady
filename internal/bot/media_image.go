// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	maxMediaBytes  = 20 << 20
	maxMediaFrames = 1800
	mediaFrameSize = 512
)

func runMediaCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = time.Second
	output, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s: %w", name, ctx.Err())
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return output, nil
}

func resolveMediaTools(ffmpegPath string) (string, string, error) {
	ffmpegPath = strings.TrimSpace(ffmpegPath)
	if ffmpegPath == "" {
		return "", "", fmt.Errorf("ffmpeg_path is unset")
	}
	executable, err := exec.LookPath(ffmpegPath)
	if err != nil {
		return "", "", fmt.Errorf("resolving configured FFmpeg: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", "", fmt.Errorf("resolving absolute FFmpeg path: %w", err)
	}
	probeName := "ffprobe"
	if strings.EqualFold(filepath.Ext(executable), ".exe") {
		probeName += ".exe"
	}
	probe, err := exec.LookPath(filepath.Join(filepath.Dir(executable), probeName))
	if err != nil {
		return "", "", fmt.Errorf("resolving ffprobe alongside configured FFmpeg: %w", err)
	}
	return executable, probe, nil
}

// prepareMediaImage renders a sticker or a chronological contact sheet.
// FFmpeg handles GIF disposal/compositing and Telegram's MP4/WebM animations.
func prepareMediaImage(ctx context.Context, ffmpegPath string, data []byte, animated bool) ([]byte, error) {
	if len(data) == 0 || len(data) > maxMediaBytes {
		return nil, fmt.Errorf("media must contain 1 to %d bytes", maxMediaBytes)
	}
	ffmpeg, ffprobe, err := resolveMediaTools(ffmpegPath)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	dir, err := os.MkdirTemp("", "gennadium-media-")
	if err != nil {
		return nil, fmt.Errorf("creating media workspace: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("Error cleaning up media workspace %s: %v", dir, err)
		}
	}()
	input := filepath.Join(dir, "input")
	output := filepath.Join(dir, "output.png")
	if err := os.WriteFile(input, data, 0600); err != nil {
		return nil, fmt.Errorf("writing media: %w", err)
	}
	probeData, err := runMediaCommand(ctx, ffprobe,
		"-v", "error", "-threads", "1", "-protocol_whitelist", "file,pipe",
		"-select_streams", "v:0", "-count_frames", "-read_intervals", fmt.Sprintf("%%+#%d", maxMediaFrames+1),
		"-show_entries", "stream=codec_name,width,height,nb_read_frames", "-of", "json", input)
	if err != nil {
		return nil, err
	}
	var probe struct {
		Streams []struct {
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Frames string `json:"nb_read_frames"`
			Codec  string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(probeData, &probe); err != nil {
		return nil, fmt.Errorf("decoding media metadata: %w", err)
	}
	if len(probe.Streams) != 1 {
		return nil, fmt.Errorf("media has no video/image stream")
	}
	stream := probe.Streams[0]
	if stream.Width <= 0 || stream.Height <= 0 || stream.Width > 4096 || stream.Height > 4096 {
		return nil, fmt.Errorf("unsupported media dimensions %dx%d (maximum 4096x4096)", stream.Width, stream.Height)
	}
	count, err := strconv.Atoi(stream.Frames)
	if err != nil || count < 1 || count > maxMediaFrames {
		return nil, fmt.Errorf("unsupported media frame count %q (maximum %d)", stream.Frames, maxMediaFrames)
	}

	indices := []int{0}
	if animated && count > 2 {
		indices = append(indices, (count-1)/2)
	}
	if animated && count > 1 {
		indices = append(indices, count-1)
	}
	selections := make([]string, len(indices))
	for i, index := range indices {
		selections[i] = fmt.Sprintf("eq(n\\,%d)", index)
	}
	filter := fmt.Sprintf(
		"select=%s,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=white,tile=%dx1:nb_frames=%d:color=white",
		strings.Join(selections, "+"), mediaFrameSize, mediaFrameSize, mediaFrameSize, mediaFrameSize, len(indices), len(indices))
	args := []string{
		"-nostdin", "-v", "error", "-threads", "1", "-filter_threads", "1",
		"-protocol_whitelist", "file,pipe",
	}
	// FFmpeg's native VP8/VP9 decoders discard transparent sticker alpha planes.
	switch stream.Codec {
	case "vp9":
		args = append(args, "-c:v", "libvpx-vp9")
	case "vp8":
		args = append(args, "-c:v", "libvpx")
	}
	args = append(args, "-i", input, "-map", "0:v:0",
		"-vf", filter, "-frames:v", "1", "-threads", "1", "-y", output)
	_, err = runMediaCommand(ctx, ffmpeg, args...)
	if err != nil {
		return nil, err
	}
	pngData, err := os.ReadFile(output)
	if err != nil {
		return nil, fmt.Errorf("reading rendered media: %w", err)
	}
	rendered, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return nil, fmt.Errorf("decoding rendered media: %w", err)
	}
	// White backing keeps transparent sticker text visible to every provider.
	flattened := image.NewRGBA(rendered.Bounds())
	draw.Draw(flattened, flattened.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flattened, flattened.Bounds(), rendered, rendered.Bounds().Min, draw.Over)
	var result bytes.Buffer
	if err := png.Encode(&result, flattened); err != nil {
		return nil, fmt.Errorf("encoding rendered media: %w", err)
	}
	return result.Bytes(), nil
}
