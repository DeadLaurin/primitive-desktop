package primitive

import (
	"errors"
	"image"
	"image/color"
	"image/gif"
	"math"
	"os"
	"os/exec"

	"github.com/fogleman/gg"
	"github.com/soniakeys/quant"
	"github.com/soniakeys/quant/median"
)

// gifSampleTarget is roughly how many pixels are sampled across all frames when
// building the shared colour palette.
const gifSampleTarget = 200000

// GIFSettings configures the built-in pure-Go animated GIF encoder.
type GIFSettings struct {
	// MaxColors is the size of the global colour table, 2..256.
	MaxColors int
	// Dither applies Floyd-Steinberg error diffusion when mapping frames onto
	// the shared palette. It costs some quality in flat areas and noticeably
	// improves smooth gradients.
	Dither bool
	// Delay is the per-frame delay in hundredths of a second.
	Delay int
	// LastDelay is the delay for the final frame, so the loop pauses there.
	LastDelay int
}

// DefaultGIFSettings returns the settings used when none are supplied.
//
// Dithering is off by default: these images are made of flat shapes, where
// error diffusion only adds visible grain and roughly doubles the file size.
func DefaultGIFSettings() GIFSettings {
	return GIFSettings{
		MaxColors: 256,
		Dither:    false,
		Delay:     50,
		LastDelay: 250,
	}
}

// ImageMagickCommand returns the path of an ImageMagick binary, or "" when
// ImageMagick is not installed.
func ImageMagickCommand() string {
	for _, name := range []string{"magick", "convert"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}

// FramesAtSize renders the shape history into animation frames no larger than
// maxDim on their longest edge.
//
// Unlike Frames, which renders at full output resolution and returns every
// frame, this draws directly at the target size and keeps at most maxFrames
// frames evenly spaced through the animation. A 500-shape run at 1024px would
// otherwise need gigabytes for the intermediate frames.
//
// maxFrames <= 0 keeps every frame.
func (model *Model) FramesAtSize(maxDim, maxFrames int) []image.Image {
	sw, sh := model.Sw, model.Sh
	if sw < 1 || sh < 1 {
		return nil
	}

	scale := 1.0
	if maxDim > 0 {
		if longest := maxInt(sw, sh); longest > maxDim {
			scale = float64(maxDim) / float64(longest)
		}
	}
	tw := maxInt(1, int(math.Round(float64(sw)*scale)))
	th := maxInt(1, int(math.Round(float64(sh)*scale)))

	dc := gg.NewContext(tw, th)
	dc.Scale(model.Scale*scale, model.Scale*scale)
	dc.Translate(0.5, 0.5)
	dc.SetColor(model.Background.NRGBA())
	dc.Clear()

	n := len(model.Shapes)

	// Decide which steps produce a frame. Step 0 is the blank background and
	// step n is the finished picture, so both are always kept.
	take := make([]bool, n+1)
	if maxFrames <= 0 || n+1 <= maxFrames {
		for i := range take {
			take[i] = true
		}
	} else if maxFrames == 1 {
		take[0] = true
	} else {
		for k := 0; k < maxFrames; k++ {
			idx := int(math.Round(float64(k) * float64(n) / float64(maxFrames-1)))
			take[clampInt(idx, 0, n)] = true
		}
	}
	take[0] = true
	take[n] = true

	var frames []image.Image
	if take[0] {
		// imageToRGBA copies, because the context reuses its pixel buffer.
		frames = append(frames, imageToRGBA(dc.Image()))
	}
	for i, shape := range model.Shapes {
		c := model.Colors[i]
		dc.SetRGBA255(c.R, c.G, c.B, c.A)
		shape.Draw(dc, model.Scale*scale)
		dc.Fill()
		if take[i+1] {
			frames = append(frames, imageToRGBA(dc.Image()))
		}
	}
	return frames
}

// gifPaletteSample builds a small image containing pixels sampled from every
// frame, so a single median-cut palette can be computed for the whole animation.
func gifPaletteSample(frames []image.Image) image.Image {
	bounds := frames[0].Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil
	}
	total := bounds.Dx() * bounds.Dy()

	perFrame := gifSampleTarget / len(frames)
	if perFrame < 256 {
		perFrame = 256
	}
	if perFrame > total {
		perFrame = total
	}
	stride := total / perFrame
	if stride < 1 {
		stride = 1
	}

	sample := image.NewRGBA(image.Rect(0, 0, perFrame, len(frames)))
	for row, frame := range frames {
		b := frame.Bounds()
		w := b.Dx()
		if w <= 0 {
			continue
		}
		n := 0
		for i := 0; i < total && n < perFrame; i += stride {
			x := b.Min.X + i%w
			y := b.Min.Y + i/w
			if x >= b.Max.X || y >= b.Max.Y {
				continue
			}
			sample.Set(n, row, frame.At(x, y))
			n++
		}
	}
	return sample
}

// SharedGIFPalette computes one palette for every frame. Using a single global
// colour table keeps colours stable across the animation instead of letting
// them shift from frame to frame.
func SharedGIFPalette(frames []image.Image, maxColors int) quant.Palette {
	if len(frames) == 0 {
		return nil
	}
	if maxColors < 2 {
		maxColors = 2
	}
	if maxColors > 256 {
		maxColors = 256
	}
	sample := gifPaletteSample(frames)
	if sample == nil {
		return nil
	}
	return median.Quantizer(maxColors).Palette(sample)
}

// palettedGIFDither maps a frame onto pal using Floyd-Steinberg error diffusion.
func palettedGIFDither(src image.Image, pal quant.Palette) *image.Paletted {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewPaletted(b, pal.ColorPalette())

	cur := make([]float64, w*h*3)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*w + x) * 3
			cur[i] = float64(r >> 8)
			cur[i+1] = float64(g >> 8)
			cur[i+2] = float64(bl >> 8)
		}
	}

	diffuse := func(x, y int, er, eg, eb, factor float64) {
		if x < 0 || x >= w || y < 0 || y >= h {
			return
		}
		i := (y*w + x) * 3
		cur[i] += er * factor
		cur[i+1] += eg * factor
		cur[i+2] += eb * factor
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			cr := clamp(cur[i], 0, 255)
			cg := clamp(cur[i+1], 0, 255)
			cb := clamp(cur[i+2], 0, 255)

			c := color.RGBA{uint8(cr + 0.5), uint8(cg + 0.5), uint8(cb + 0.5), 255}
			idx := pal.IndexNear(c)
			dst.SetColorIndex(b.Min.X+x, b.Min.Y+y, uint8(idx))

			pr, pg, pb, _ := pal.ColorNear(c).RGBA()
			er := cr - float64(pr>>8)
			eg := cg - float64(pg>>8)
			eb := cb - float64(pb>>8)

			diffuse(x+1, y, er, eg, eb, 7.0/16)
			diffuse(x-1, y+1, er, eg, eb, 3.0/16)
			diffuse(x, y+1, er, eg, eb, 5.0/16)
			diffuse(x+1, y+1, er, eg, eb, 1.0/16)
		}
	}
	return dst
}

// SaveGIFGo writes an animated GIF using only Go code, with a global palette
// shared by every frame. It needs no external tools, unlike SaveGIFImageMagick.
func SaveGIFGo(path string, frames []image.Image, settings GIFSettings) error {
	if len(frames) == 0 {
		return errors.New("no frames to encode")
	}
	if settings.MaxColors < 2 {
		settings.MaxColors = 256
	}

	pal := SharedGIFPalette(frames, settings.MaxColors)
	if pal == nil || pal.Len() == 0 {
		return errors.New("could not build a colour palette for the animation")
	}
	shared := pal.ColorPalette()

	bounds := frames[0].Bounds()
	g := &gif.GIF{
		Image:     make([]*image.Paletted, 0, len(frames)),
		Delay:     make([]int, 0, len(frames)),
		Disposal:  make([]byte, 0, len(frames)),
		LoopCount: 0, // loop forever
		Config: image.Config{
			ColorModel: shared,
			Width:      bounds.Dx(),
			Height:     bounds.Dy(),
		},
	}

	last := len(frames) - 1
	for i, frame := range frames {
		var pm *image.Paletted
		if settings.Dither {
			pm = palettedGIFDither(frame, pal)
		} else {
			pm = quant.Paletted(pal, frame)
		}
		if pm == nil {
			return errors.New("could not map a frame onto the colour palette")
		}
		// Share the palette's backing array so the encoder reuses the global
		// colour table instead of writing one per frame.
		pm.Palette = shared
		g.Image = append(g.Image, pm)
		g.Disposal = append(g.Disposal, gif.DisposalNone)

		delay := settings.Delay
		if i == last && settings.LastDelay > 0 {
			delay = settings.LastDelay
		}
		g.Delay = append(g.Delay, delay)
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return gif.EncodeAll(file, g)
}
