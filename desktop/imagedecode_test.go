//go:build darwin && cgo

package main

import (
	"bytes"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeHEICSample converts the bundled example image to HEIC using the system
// sips tool, so the test does not need a checked-in HEIC binary.
func makeHEICSample(t *testing.T, dir string) string {
	t.Helper()

	src := filepath.Join("..", "examples", "monalisa.png")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("example image not available: %v", err)
	}
	if _, err := exec.LookPath("sips"); err != nil {
		t.Skip("sips is not available to create a HEIC sample")
	}

	out := filepath.Join(dir, "sample.heic")
	if msg, err := exec.Command("sips", "-s", "format", "heic", src, "--out", out).CombinedOutput(); err != nil {
		t.Skipf("could not create a HEIC sample: %v: %s", err, msg)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Skip("no HEIC sample was produced")
	}
	return out
}

// TestPlatformDecodeHEIC verifies that HEIF/HEIC files, which the Go standard
// library cannot read, decode through macOS ImageIO.
func TestPlatformDecodeHEIC(t *testing.T) {
	heic := makeHEICSample(t, t.TempDir())

	data, err := os.ReadFile(heic)
	if err != nil {
		t.Fatalf("read sample: %v", err)
	}

	// Guard the premise of this test: the standard library really cannot do it.
	if _, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		t.Skip("the standard library decoded the HEIC, so the platform path is untested")
	}

	img, err := platformDecode(data)
	if err != nil {
		t.Fatalf("platformDecode: %v", err)
	}
	if img.Bounds().Dx() == 0 || img.Bounds().Dy() == 0 {
		t.Fatalf("decoded a %v image", img.Bounds())
	}
	t.Logf("decoded HEIC: %v", img.Bounds())
}

// TestLoadImageHandlesHEIC checks the whole path the app uses: loadImage must
// transparently fall back to the platform decoder for a .heic file.
func TestLoadImageHandlesHEIC(t *testing.T) {
	dir := t.TempDir()
	heic := makeHEICSample(t, dir)

	heicImg, err := loadImage(heic)
	if err != nil {
		t.Fatalf("loadImage(heic): %v", err)
	}

	reference, err := loadImage(filepath.Join("..", "examples", "monalisa.png"))
	if err != nil {
		t.Fatalf("loadImage(png): %v", err)
	}

	if heicImg.Bounds() != reference.Bounds() {
		t.Fatalf("HEIC decoded to %v, reference is %v", heicImg.Bounds(), reference.Bounds())
	}

	// HEIC is lossy, so individual pixels can differ a lot: decoding the same
	// file with macOS' own sips gives a mean absolute difference of ~3.4 against
	// the source PNG, with a worst case over 100. The mean is therefore the
	// meaningful check; it would move sharply if a channel were swapped or the
	// colour space were mishandled.
	var sum, count, worst int
	b := reference.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			hr, hg, hb, _ := heicImg.At(x, y).RGBA()
			rr, rg, rb, _ := reference.At(x, y).RGBA()
			for _, d := range []int{
				int(hr>>8) - int(rr>>8),
				int(hg>>8) - int(rg>>8),
				int(hb>>8) - int(rb>>8),
			} {
				if d < 0 {
					d = -d
				}
				sum += d
				count++
				if d > worst {
					worst = d
				}
			}
		}
	}
	if count == 0 {
		t.Fatal("no pixels were compared")
	}

	mean := float64(sum) / float64(count)
	t.Logf("compared %d channel values: mean abs diff %.2f, worst %d", count, mean, worst)

	if mean > 8 {
		t.Errorf("mean absolute difference %.2f is too high for lossy HEIC (expect ~3-4)", mean)
	}
	if worst > 180 {
		t.Errorf("worst-case difference %d suggests a decoding fault, not compression", worst)
	}
}

// TestLoadImageRejectsGarbage makes sure a broken file produces an error rather
// than a panic or a zero-sized image.
func TestLoadImageRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-image.heic")
	if err := os.WriteFile(path, []byte("this is definitely not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadImage(path); err == nil {
		t.Fatal("expected an error for a non-image file")
	}
}
