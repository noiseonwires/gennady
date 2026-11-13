// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2026 Kirill aka Noiseonwires
package bot

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func requireMediaTools(t *testing.T) string {
	t.Helper()
	path := os.Getenv("GENNADIUM_TEST_FFMPEG_PATH")
	if path == "" {
		path = "ffmpeg"
	}
	ffmpeg, _, err := resolveMediaTools(path)
	if err != nil {
		t.Skipf("media conversion tests require FFmpeg and sibling ffprobe: %v", err)
	}
	return ffmpeg
}

func mediaTestGIF(t *testing.T, frames int) []byte {
	t.Helper()
	palette := color.Palette{
		color.RGBA{R: 255, A: 255}, color.RGBA{R: 255, G: 255, A: 255},
		color.RGBA{G: 255, A: 255}, color.RGBA{G: 255, B: 255, A: 255},
		color.RGBA{B: 255, A: 255},
	}
	animation := &gif.GIF{}
	for i := 0; i < frames; i++ {
		frame := image.NewPaletted(image.Rect(0, 0, 8, 8), palette)
		for p := range frame.Pix {
			frame.Pix[p] = uint8(i % len(palette))
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 10)
	}
	var data bytes.Buffer
	require.NoError(t, gif.EncodeAll(&data, animation))
	return data.Bytes()
}

func mediaTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	img.Set(4, 4, color.RGBA{R: 255, A: 255})
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, img))
	return data.Bytes()
}

func TestPrepareMediaImage_FirstMiddleLast(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	t.Setenv("PATH", t.TempDir())
	source := mediaTestGIF(t, 5)
	for _, format := range []string{"gif", "mp4", "webm"} {
		t.Run(format, func(t *testing.T) {
			data := source
			if format != "gif" {
				dir := t.TempDir()
				input := filepath.Join(dir, "input.gif")
				output := filepath.Join(dir, "output."+format)
				require.NoError(t, os.WriteFile(input, source, 0600))
				codec := "libx264"
				if format == "webm" {
					codec = "libvpx-vp9"
				}
				_, err := runMediaCommand(context.Background(), ffmpeg, "-v", "error", "-i", input,
					"-c:v", codec, "-pix_fmt", "yuv420p", "-fps_mode", "vfr", output)
				require.NoError(t, err)
				data, err = os.ReadFile(output)
				require.NoError(t, err)
			}
			prepared, err := prepareMediaImage(context.Background(), ffmpeg, data, true)
			require.NoError(t, err)
			img, err := png.Decode(bytes.NewReader(prepared))
			require.NoError(t, err)
			assert.Equal(t, image.Rect(0, 0, 3*mediaFrameSize, mediaFrameSize), img.Bounds())
			for i, want := range []color.RGBA{{R: 255, A: 255}, {G: 255, A: 255}, {B: 255, A: 255}} {
				got := color.RGBAModel.Convert(img.At(i*mediaFrameSize+256, 256)).(color.RGBA)
				assert.InDelta(t, want.R, got.R, 10)
				assert.InDelta(t, want.G, got.G, 10)
				assert.InDelta(t, want.B, got.B, 10)
			}
		})
	}
}

func TestPrepareMediaImage_ShortAnimations(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	for _, count := range []int{1, 2} {
		prepared, err := prepareMediaImage(context.Background(), ffmpeg, mediaTestGIF(t, count), true)
		require.NoError(t, err)
		img, err := png.Decode(bytes.NewReader(prepared))
		require.NoError(t, err)
		assert.Equal(t, count*mediaFrameSize, img.Bounds().Dx())
	}
}

func TestPrepareMediaImage_StaticWebPTransparency(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.png"), filepath.Join(dir, "sticker.webp")
	require.NoError(t, os.WriteFile(input, mediaTestPNG(t), 0600))
	_, err := runMediaCommand(context.Background(), ffmpeg, "-v", "error", "-i", input, "-lossless", "1", output)
	require.NoError(t, err)
	data, err := os.ReadFile(output)
	require.NoError(t, err)
	prepared, err := prepareMediaImage(context.Background(), ffmpeg, data, false)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(prepared))
	require.NoError(t, err)
	assert.Equal(t, image.Rect(0, 0, mediaFrameSize, mediaFrameSize), img.Bounds())
	assert.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, color.RGBAModel.Convert(img.At(0, 0)))
}

func TestPrepareMediaImage_InvalidInput(t *testing.T) {
	_, err := prepareMediaImage(context.Background(), "ffmpeg", nil, false)
	require.ErrorContains(t, err, "media must contain")
	_, err = prepareMediaImage(context.Background(), "ffmpeg", make([]byte, maxMediaBytes+1), false)
	require.ErrorContains(t, err, "media must contain")
	ffmpeg := requireMediaTools(t)
	_, err = prepareMediaImage(context.Background(), ffmpeg, []byte("not an image"), true)
	require.Error(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = prepareMediaImage(ctx, ffmpeg, mediaTestPNG(t), false)
	require.ErrorIs(t, err, context.Canceled)
}

func TestPrepareMediaImage_RejectsExcessiveFrameCount(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	_, err := prepareMediaImage(context.Background(), ffmpeg, mediaTestGIF(t, maxMediaFrames+1), true)
	require.ErrorContains(t, err, "unsupported media frame count")
}

func TestPrepareMediaImage_GIFCompositingAndDisposal(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	palette := color.Palette{
		color.Transparent, color.RGBA{R: 255, A: 255},
		color.RGBA{G: 255, A: 255}, color.RGBA{B: 255, A: 255},
	}
	frames := []*image.Paletted{
		image.NewPaletted(image.Rect(0, 0, 8, 8), palette),
		image.NewPaletted(image.Rect(0, 0, 4, 8), palette),
		image.NewPaletted(image.Rect(4, 0, 8, 8), palette),
	}
	for i, frame := range frames {
		for p := range frame.Pix {
			frame.Pix[p] = uint8(i + 1)
		}
	}
	var source bytes.Buffer
	require.NoError(t, gif.EncodeAll(&source, &gif.GIF{
		Image: frames, Delay: []int{10, 10, 10},
		Disposal: []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
	}))
	prepared, err := prepareMediaImage(context.Background(), ffmpeg, source.Bytes(), true)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(prepared))
	require.NoError(t, err)
	assert.Equal(t, color.RGBA{G: 255, A: 255}, color.RGBAModel.Convert(img.At(512+128, 256)))
	assert.Equal(t, color.RGBA{R: 255, A: 255}, color.RGBAModel.Convert(img.At(512+384, 256)))
	assert.Equal(t, color.RGBA{R: 255, A: 255}, color.RGBAModel.Convert(img.At(1024+128, 256)))
	assert.Equal(t, color.RGBA{B: 255, A: 255}, color.RGBAModel.Convert(img.At(1024+384, 256)))
}

func TestPrepareMediaImage_RejectsExcessiveDimensions(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	var source bytes.Buffer
	require.NoError(t, png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 4097, 1))))
	_, err := prepareMediaImage(context.Background(), ffmpeg, source.Bytes(), false)
	require.ErrorContains(t, err, "unsupported media dimensions")
}

func TestRunMediaCommand_MissingTool(t *testing.T) {
	_, err := runMediaCommand(context.Background(), "gennadium-nonexistent-media-converter")
	require.ErrorContains(t, err, "gennadium-nonexistent-media-converter")
}

func TestPrepareMediaImage_VideoStickerTransparency(t *testing.T) {
	ffmpeg := requireMediaTools(t)
	dir := t.TempDir()
	input, output := filepath.Join(dir, "input.png"), filepath.Join(dir, "sticker.webm")
	require.NoError(t, os.WriteFile(input, mediaTestPNG(t), 0600))
	_, err := runMediaCommand(context.Background(), ffmpeg, "-v", "error", "-i", input,
		"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-lossless", "1", output)
	require.NoError(t, err)
	source, err := os.ReadFile(output)
	require.NoError(t, err)
	prepared, err := prepareMediaImage(context.Background(), ffmpeg, source, true)
	require.NoError(t, err)
	img, err := png.Decode(bytes.NewReader(prepared))
	require.NoError(t, err)
	assert.Equal(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}, color.RGBAModel.Convert(img.At(0, 0)))
}

func TestResolveMediaTools_ConfiguredPathAndSiblingProbe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "media tools with spaces")
	require.NoError(t, os.Mkdir(dir, 0700))
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	ffmpeg := filepath.Join(dir, "custom-ffmpeg"+suffix)
	ffprobe := filepath.Join(dir, "ffprobe"+suffix)
	require.NoError(t, os.WriteFile(ffmpeg, []byte("placeholder"), 0700))
	t.Setenv("PATH", t.TempDir())

	_, _, err := resolveMediaTools(ffmpeg)
	require.ErrorContains(t, err, "ffprobe alongside configured FFmpeg")
	require.NoError(t, os.WriteFile(ffprobe, []byte("placeholder"), 0700))
	gotFFmpeg, gotFFprobe, err := resolveMediaTools("  " + ffmpeg + "  ")
	require.NoError(t, err)
	assert.Equal(t, ffmpeg, gotFFmpeg)
	assert.Equal(t, ffprobe, gotFFprobe)

	t.Setenv("PATH", dir)
	gotFFmpeg, gotFFprobe, err = resolveMediaTools("custom-ffmpeg")
	require.NoError(t, err)
	assert.Equal(t, ffmpeg, gotFFmpeg)
	assert.Equal(t, ffprobe, gotFFprobe)
}

func TestPrepareMediaImage_UnsetOrInvalidToolPath(t *testing.T) {
	for _, path := range []string{"", " \t "} {
		_, err := prepareMediaImage(context.Background(), path, mediaTestPNG(t), false)
		require.ErrorContains(t, err, "ffmpeg_path is unset")
	}
	_, err := prepareMediaImage(context.Background(), filepath.Join(t.TempDir(), "missing-ffmpeg"), mediaTestPNG(t), false)
	require.ErrorContains(t, err, "resolving configured FFmpeg")
}
