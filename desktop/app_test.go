package main

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fogleman/primitive/primitive"
	"github.com/nfnt/resize"
)

// TestPipelineProducesOutput exercises the same code path App.Start uses:
// load -> resize -> background colour -> model -> steps -> save.
func TestPipelineProducesOutput(t *testing.T) {
	src := filepath.Join("..", "examples", "monalisa.png")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("example image not available: %v", err)
	}

	img, err := primitive.LoadImage(src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	img = resize.Thumbnail(128, 128, img, resize.Bilinear)

	bg := primitive.MakeColor(primitive.AverageImageColor(img))
	model := primitive.NewModel(img, bg, 256, 2)

	start := model.Score
	for i := 0; i < 10; i++ {
		// Step returns the energy evaluations performed for this step only;
		// each worker's counter is reset by Worker.Init at the start of a step.
		if n := model.Step(primitive.ShapeTypeTriangle, 128, 0); n <= 0 {
			t.Fatalf("step %d reported %d energy evaluations, want > 0", i, n)
		}
	}

	if model.Score >= start {
		t.Fatalf("score did not improve: %v -> %v", start, model.Score)
	}
	if len(model.Shapes) != 10 {
		t.Fatalf("want 10 shapes, got %d", len(model.Shapes))
	}

	dir := t.TempDir()

	png := filepath.Join(dir, "out.png")
	if err := primitive.SavePNG(png, model.Context.Image()); err != nil {
		t.Fatalf("save png: %v", err)
	}
	if fi, err := os.Stat(png); err != nil || fi.Size() == 0 {
		t.Fatalf("png not written: %v", err)
	}

	svg := filepath.Join(dir, "out.svg")
	if err := primitive.SaveFile(svg, model.SVG()); err != nil {
		t.Fatalf("save svg: %v", err)
	}
	if fi, err := os.Stat(svg); err != nil || fi.Size() == 0 {
		t.Fatalf("svg not written: %v", err)
	}

	jpg := filepath.Join(dir, "out.jpg")
	if err := primitive.SaveJPG(jpg, model.Context.Image(), 95); err != nil {
		t.Fatalf("save jpg: %v", err)
	}
}

// TestEvaluationsPerSecond guards the shape-rate calculation shown in the UI.
func TestEvaluationsPerSecond(t *testing.T) {
	if got := evaluationsPerSecond(1000, 500*time.Millisecond); got != 2000 {
		t.Fatalf("got %v, want 2000", got)
	}
	if got := evaluationsPerSecond(100, 0); got != 0 {
		t.Fatalf("zero duration should yield 0, got %v", got)
	}
	if got := evaluationsPerSecond(0, time.Second); got != 0 {
		t.Fatalf("zero count should yield 0, got %v", got)
	}
}

// TestEncodeDataURL checks the preview transport format.
func TestEncodeDataURL(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	got := encodeDataURL(img, 32)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("unexpected data URL prefix: %.40s", got)
	}
	if len(got) < 100 {
		t.Fatalf("data URL suspiciously short: %d bytes", len(got))
	}
}
