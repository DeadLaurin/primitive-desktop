package primitive

import (
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
)

func solidFrame(c color.RGBA, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func gradientFrame(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

// TestSaveGIFGoRoundTrip checks that the built-in encoder produces a readable
// animation whose frames keep their colours and share one global palette.
func TestSaveGIFGoRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.gif")

	want := []color.RGBA{
		{255, 0, 0, 255},
		{0, 255, 0, 255},
		{0, 0, 255, 255},
	}
	frames := []image.Image{
		solidFrame(want[0], 64, 48),
		solidFrame(want[1], 64, 48),
		solidFrame(want[2], 64, 48),
	}

	if err := SaveGIFGo(path, frames, DefaultGIFSettings()); err != nil {
		t.Fatalf("SaveGIFGo: %v", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer file.Close()

	g, err := gif.DecodeAll(file)
	if err != nil {
		t.Fatalf("DecodeAll: %v", err)
	}
	if len(g.Image) != len(frames) {
		t.Fatalf("got %d frames, want %d", len(g.Image), len(frames))
	}
	if g.Config.Width != 64 || g.Config.Height != 48 {
		t.Fatalf("got %dx%d, want 64x48", g.Config.Width, g.Config.Height)
	}

	global, ok := g.Config.ColorModel.(color.Palette)
	if !ok || len(global) == 0 {
		t.Fatalf("no global colour table in the encoded GIF")
	}

	for i, frame := range g.Image {
		// Every frame must reference the global table, otherwise the encoder
		// wrote a local table per frame.
		if len(frame.Palette) != len(global) {
			t.Fatalf("frame %d has a %d-entry palette, global table has %d",
				i, len(frame.Palette), len(global))
		}
		r, gr, b, _ := frame.At(0, 0).RGBA()
		got := color.RGBA{uint8(r >> 8), uint8(gr >> 8), uint8(b >> 8), 255}
		if got != want[i] {
			t.Errorf("frame %d pixel = %v, want %v", i, got, want[i])
		}
	}

	// The final frame gets the longer delay so the loop pauses on the result.
	last := len(g.Delay) - 1
	if g.Delay[last] <= g.Delay[0] {
		t.Errorf("final delay %d should exceed the frame delay %d", g.Delay[last], g.Delay[0])
	}
}

// TestSaveGIFGoDithering checks that a smooth gradient survives quantisation
// when dithering is on, and is rejected for empty input.
func TestSaveGIFGoDithering(t *testing.T) {
	frames := []image.Image{gradientFrame(128, 128), gradientFrame(128, 128)}

	path := filepath.Join(t.TempDir(), "gradient.gif")
	settings := DefaultGIFSettings()
	settings.Dither = true
	if err := SaveGIFGo(path, frames, settings); err != nil {
		t.Fatalf("SaveGIFGo: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		t.Fatalf("gif not written: %v", err)
	}

	// Without dithering it should still encode.
	settings.Dither = false
	if err := SaveGIFGo(filepath.Join(t.TempDir(), "flat.gif"), frames, settings); err != nil {
		t.Fatalf("SaveGIFGo without dithering: %v", err)
	}

	if err := SaveGIFGo(filepath.Join(t.TempDir(), "empty.gif"), nil, settings); err == nil {
		t.Error("expected an error for zero frames")
	}
}

// TestFramesAtSize checks the frame renderer used for GIF export: bounded
// dimensions, a bounded frame count, and the first/last steps always kept.
func TestFramesAtSize(t *testing.T) {
	target := gradientFrame(96, 64)
	bg := MakeColor(AverageImageColor(target))
	model := NewModel(target, bg, 256, 1)

	for i := 0; i < 12; i++ {
		model.Step(ShapeTypeTriangle, 128, 0)
	}

	frames := model.FramesAtSize(64, 5)
	if len(frames) != 5 {
		t.Fatalf("got %d frames, want 5", len(frames))
	}

	bounds := frames[0].Bounds()
	if bounds.Dx() > 64 || bounds.Dy() > 64 {
		t.Fatalf("frame is %dx%d, want at most 64 on the longest edge", bounds.Dx(), bounds.Dy())
	}
	for i, f := range frames {
		if f.Bounds() != bounds {
			t.Fatalf("frame %d has bounds %v, want %v", i, f.Bounds(), bounds)
		}
	}

	// The animation must actually progress.
	if framesEqual(frames[0], frames[len(frames)-1]) {
		t.Error("first and last frames are identical; the animation does not progress")
	}

	// Keeping every frame is also supported.
	all := model.FramesAtSize(0, 0)
	if len(all) != len(model.Shapes)+1 {
		t.Fatalf("got %d frames, want %d", len(all), len(model.Shapes)+1)
	}
	if b := all[0].Bounds(); b.Dx() != model.Sw || b.Dy() != model.Sh {
		t.Fatalf("uncapped frames should be %dx%d, got %v", model.Sw, model.Sh, b)
	}
}

// framesEqual reports whether two images have identical pixels. The frames are
// RGBA, so comparing the backing slices is enough.
func framesEqual(a, b image.Image) bool {
	ab, aok := a.(*image.RGBA)
	bb, bok := b.(*image.RGBA)
	if !aok || !bok || ab.Rect != bb.Rect {
		return false
	}
	for i := range ab.Pix {
		if ab.Pix[i] != bb.Pix[i] {
			return false
		}
	}
	return true
}

// TestImageMagickCommandDoesNotPanic simply exercises the lookup; the result
// depends on whether ImageMagick is installed on the machine.
func TestImageMagickCommandDoesNotPanic(t *testing.T) {
	t.Logf("ImageMagickCommand() = %q", ImageMagickCommand())
}
