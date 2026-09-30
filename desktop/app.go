package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/fogleman/primitive/primitive"
	"github.com/nfnt/resize"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	// previewMaxDim is the longest edge of the streamed live preview.
	previewMaxDim = 512
	// previewThrottle limits how often a preview frame is encoded and sent.
	previewThrottle = 90 * time.Millisecond
	// targetMaxDim is the longest edge of the selected-image thumbnail.
	targetMaxDim = 420
)

// RunConfig holds every user-tunable option, mirroring the CLI flags.
type RunConfig struct {
	InputPath  string `json:"inputPath"`
	Count      int    `json:"count"`
	Mode       int    `json:"mode"`
	Alpha      int    `json:"alpha"`
	Repeat     int    `json:"repeat"`
	InputSize  int    `json:"inputSize"`
	OutputSize int    `json:"outputSize"`
	Background string `json:"background"`
	Workers    int    `json:"workers"`
}

// InputInfo describes the currently selected target image.
type InputInfo struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Preview string `json:"preview"`
}

// Progress is emitted as "run:progress" for every shape that is added.
type Progress struct {
	RunID   int64   `json:"runId"`
	Frame   int     `json:"frame"`
	Total   int     `json:"total"`
	Score   float64 `json:"score"`
	NPS     float64 `json:"nps"`
	Elapsed float64 `json:"elapsed"`
	Preview string  `json:"preview,omitempty"`
}

// GIFExport configures an animated GIF export.
type GIFExport struct {
	// Engine is "go" for the built-in encoder or "imagemagick" to shell out.
	Engine string `json:"engine"`
	// MaxSize is the longest edge of the rendered frames, in pixels.
	MaxSize int `json:"maxSize"`
	// MaxFrames caps how many frames are kept.
	MaxFrames int `json:"maxFrames"`
	// Dither enables Floyd-Steinberg error diffusion. Off by default, because
	// flat-shaded shapes gain nothing but grain and file size from it.
	Dither bool `json:"dither"`
}

// GIFEncoders reports which GIF encoders are usable on this machine.
type GIFEncoders struct {
	BuiltIn         bool   `json:"builtIn"`
	ImageMagickPath string `json:"imageMagickPath"`
	ImageMagick     bool   `json:"imageMagick"`
}

// Status lets the frontend resynchronise with the backend on load.
type Status struct {
	Running   bool      `json:"running"`
	Frame     int       `json:"frame"`
	Total     int       `json:"total"`
	Score     float64   `json:"score"`
	HasResult bool      `json:"hasResult"`
	Input     InputInfo `json:"input"`
}

// App is the bound backend: every exported method is callable from JavaScript.
type App struct {
	ctx context.Context

	// mu guards the model and all of the progress fields below. The run
	// goroutine holds it for the duration of each model.Step call, so the
	// model is never touched concurrently.
	mu        sync.Mutex
	model     *primitive.Model
	running   bool
	cancelled bool
	runID     int64
	frame     int
	total     int
	score     float64
	inputPath string
	input     InputInfo
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved so we can call
// runtime methods, and file drops are wired up.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	wruntime.OnFileDrop(ctx, func(x, y int, paths []string) {
		if len(paths) == 0 {
			return
		}
		info, err := a.LoadInput(paths[0])
		if err != nil {
			wruntime.EventsEmit(ctx, "input:error", err.Error())
			return
		}
		wruntime.EventsEmit(ctx, "input:changed", info)
	})
}

// Status reports the current backend state.
func (a *App) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Status{
		Running:   a.running,
		Frame:     a.frame,
		Total:     a.total,
		Score:     a.score,
		HasResult: a.model != nil,
		Input:     a.input,
	}
}

// PickInput opens a native file chooser and loads the chosen image.
func (a *App) PickInput() (InputInfo, error) {
	path, err := wruntime.OpenFileDialog(a.ctx, wruntime.OpenDialogOptions{
		Title: "Choose a target image",
		Filters: []wruntime.FileFilter{
			{DisplayName: "Images", Pattern: imageFileFilter},
		},
	})
	if err != nil {
		return InputInfo{}, err
	}
	if strings.TrimSpace(path) == "" {
		// The user cancelled; hand back whatever is already loaded.
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.input, nil
	}
	return a.LoadInput(path)
}

// LoadInput decodes an image path and returns a thumbnail plus its dimensions.
func (a *App) LoadInput(path string) (InputInfo, error) {
	img, err := loadImage(path)
	if err != nil {
		return InputInfo{}, fmt.Errorf("could not open %s: %w", filepath.Base(path), err)
	}
	bounds := img.Bounds()
	info := InputInfo{
		Path:    path,
		Name:    filepath.Base(path),
		Width:   bounds.Dx(),
		Height:  bounds.Dy(),
		Preview: encodeDataURL(img, targetMaxDim),
	}

	a.mu.Lock()
	a.input = info
	a.inputPath = path
	// The previous run no longer matches this target.
	a.model = nil
	a.frame = 0
	a.total = 0
	a.score = 0
	a.mu.Unlock()

	return info, nil
}

// Input returns the currently selected target image.
func (a *App) Input() InputInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.input
}

// Start prepares a model and runs the algorithm in the background.
func (a *App) Start(cfg RunConfig) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return fmt.Errorf("a run is already in progress")
	}
	if strings.TrimSpace(cfg.InputPath) == "" {
		cfg.InputPath = a.inputPath
	}
	a.mu.Unlock()

	if strings.TrimSpace(cfg.InputPath) == "" {
		return fmt.Errorf("choose a target image first")
	}
	if cfg.Count < 1 {
		return fmt.Errorf("number of shapes must be at least 1")
	}

	// Clamp to sane values rather than failing.
	if cfg.Mode < 0 || cfg.Mode > 8 {
		cfg.Mode = 1
	}
	if cfg.Alpha < 0 {
		cfg.Alpha = 0
	}
	if cfg.Alpha > 255 {
		cfg.Alpha = 255
	}
	if cfg.Repeat < 0 {
		cfg.Repeat = 0
	}
	if cfg.InputSize < 1 {
		cfg.InputSize = 256
	}
	if cfg.OutputSize < 16 {
		cfg.OutputSize = 1024
	}
	if cfg.Workers < 1 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.Workers > 32 {
		cfg.Workers = 32
	}

	img, err := loadImage(cfg.InputPath)
	if err != nil {
		return fmt.Errorf("could not read image: %w", err)
	}
	img = resize.Thumbnail(uint(cfg.InputSize), uint(cfg.InputSize), img, resize.Bilinear)

	var bg primitive.Color
	if strings.TrimSpace(cfg.Background) == "" {
		bg = primitive.MakeColor(primitive.AverageImageColor(img))
	} else {
		bg = primitive.MakeHexColor(cfg.Background)
	}

	model := primitive.NewModel(img, bg, cfg.OutputSize, cfg.Workers)

	a.mu.Lock()
	a.runID++
	id := a.runID
	a.model = model
	a.running = true
	a.cancelled = false
	a.frame = 0
	a.total = cfg.Count
	a.score = model.Score
	a.inputPath = cfg.InputPath
	a.mu.Unlock()

	go a.run(id, model, cfg)
	return nil
}

// run adds shapes one at a time, emitting progress after each one.
func (a *App) run(id int64, model *primitive.Model, cfg RunConfig) {
	start := time.Now()
	lastPreview := time.Time{}

	// Show the blank starting canvas straight away.
	wruntime.EventsEmit(a.ctx, "run:progress", Progress{
		RunID:   id,
		Frame:   0,
		Total:   cfg.Count,
		Score:   model.Score,
		Preview: encodeDataURL(model.Context.Image(), previewMaxDim),
	})

	for i := 0; i < cfg.Count; i++ {
		a.mu.Lock()
		if a.cancelled || a.runID != id {
			a.mu.Unlock()
			break
		}
		a.mu.Unlock()

		t := time.Now()

		// The lock covers the model mutation *and* the preview encode, so the
		// model is never read while a shape is being added.
		a.mu.Lock()
		counter := model.Step(primitive.ShapeType(cfg.Mode), cfg.Alpha, cfg.Repeat)
		a.frame = i + 1
		a.score = model.Score
		frame, score := a.frame, a.score

		var preview string
		if time.Since(lastPreview) >= previewThrottle || i == cfg.Count-1 {
			preview = encodeDataURL(model.Context.Image(), previewMaxDim)
			lastPreview = time.Now()
		}
		a.mu.Unlock()

		// Step returns the number of energy evaluations for this step alone:
		// each worker's counter is reset by Worker.Init at the start of a step.
		nps := evaluationsPerSecond(counter, time.Since(t))

		wruntime.EventsEmit(a.ctx, "run:progress", Progress{
			RunID:   id,
			Frame:   frame,
			Total:   cfg.Count,
			Score:   score,
			NPS:     nps,
			Elapsed: time.Since(start).Seconds(),
			Preview: preview,
		})
	}

	a.mu.Lock()
	// Only the still-current run may clear the running flag.
	if a.runID == id {
		a.running = false
	}
	frame, score, cancelled := a.frame, a.score, a.cancelled
	a.mu.Unlock()

	wruntime.EventsEmit(a.ctx, "run:done", map[string]interface{}{
		"runId":     id,
		"cancelled": cancelled,
		"frame":     frame,
		"score":     score,
	})
}

// Stop asks the running job to finish after the current shape.
func (a *App) Stop() {
	a.mu.Lock()
	a.cancelled = true
	a.mu.Unlock()
}

// Save asks for a destination and writes the current result to it.
// Format is one of "png", "jpg" or "svg".
func (a *App) Save(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "jpeg" {
		format = "jpg"
	}
	switch format {
	case "png", "jpg", "svg":
	default:
		return "", fmt.Errorf("unsupported format: %s", format)
	}

	a.mu.Lock()
	hasResult := a.model != nil
	name := a.input.Name
	a.mu.Unlock()

	if !hasResult {
		return "", fmt.Errorf("nothing to save yet — run the algorithm first")
	}

	base := "primitive"
	if name != "" {
		base = strings.TrimSuffix(name, filepath.Ext(name)) + "-primitive"
	}

	var filters []wruntime.FileFilter
	switch format {
	case "png":
		filters = []wruntime.FileFilter{{DisplayName: "PNG image", Pattern: "*.png"}}
	case "jpg":
		filters = []wruntime.FileFilter{{DisplayName: "JPEG image", Pattern: "*.jpg;*.jpeg"}}
	case "svg":
		filters = []wruntime.FileFilter{{DisplayName: "SVG vector", Pattern: "*.svg"}}
	}

	// The dialog is shown without the lock held so the preview keeps updating.
	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:                "Save image",
		DefaultFilename:      base + "." + format,
		Filters:              filters,
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil // cancelled
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	switch format {
	case "png":
		err = primitive.SavePNG(path, a.model.Context.Image())
	case "jpg":
		err = primitive.SaveJPG(path, a.model.Context.Image(), 95)
	case "svg":
		err = primitive.SaveFile(path, a.model.SVG())
	}
	if err != nil {
		return "", err
	}
	return path, nil
}

// evaluationsPerSecond converts an evaluation count and duration into a rate.
func evaluationsPerSecond(count int, elapsed time.Duration) float64 {
	if elapsed <= 0 {
		return 0
	}
	return float64(count) / elapsed.Seconds()
}

// GIFEncoders reports which animated GIF encoders are available.
func (a *App) GIFEncoders() GIFEncoders {
	path := primitive.ImageMagickCommand()
	return GIFEncoders{
		BuiltIn:         true,
		ImageMagickPath: path,
		ImageMagick:     path != "",
	}
}

// SaveGIF renders the shape history as an animated GIF and writes it to a
// destination chosen by the user.
//
// The built-in encoder is the default. ImageMagick is used when explicitly
// requested and installed; if it fails, the built-in encoder takes over so an
// export always produces a file.
func (a *App) SaveGIF(opts GIFExport) (string, error) {
	a.mu.Lock()
	hasResult := a.model != nil
	name := a.input.Name
	a.mu.Unlock()

	if !hasResult {
		return "", fmt.Errorf("nothing to save yet — run the algorithm first")
	}

	if opts.MaxSize < 64 {
		opts.MaxSize = 480
	}
	if opts.MaxSize > 4096 {
		opts.MaxSize = 4096
	}
	if opts.MaxFrames < 2 {
		opts.MaxFrames = 100
	}
	if opts.MaxFrames > 2000 {
		opts.MaxFrames = 2000
	}

	base := "primitive"
	if name != "" {
		base = strings.TrimSuffix(name, filepath.Ext(name)) + "-primitive"
	}

	path, err := wruntime.SaveFileDialog(a.ctx, wruntime.SaveDialogOptions{
		Title:                "Save animated GIF",
		DefaultFilename:      base + ".gif",
		Filters:              []wruntime.FileFilter{{DisplayName: "Animated GIF", Pattern: "*.gif"}},
		CanCreateDirectories: true,
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", nil // cancelled
	}

	// Render the frames at the requested size. This walks the shape history
	// once, so it stays cheap even for long runs.
	a.mu.Lock()
	frames := a.model.FramesAtSize(opts.MaxSize, opts.MaxFrames)
	a.mu.Unlock()

	if len(frames) == 0 {
		return "", fmt.Errorf("there are no frames to encode")
	}

	if opts.Engine == "imagemagick" && primitive.ImageMagickCommand() != "" {
		if err := primitive.SaveGIFImageMagick(path, frames, 50, 250); err == nil {
			return path, nil
		}
		// Fall through to the built-in encoder rather than failing the export.
	}

	settings := primitive.DefaultGIFSettings()
	settings.Dither = opts.Dither
	if err := primitive.SaveGIFGo(path, frames, settings); err != nil {
		return "", err
	}
	return path, nil
}

// encodeDataURL renders an image as a data: URL, downscaling it for transport.
func encodeDataURL(im image.Image, maxDim int) string {
	bounds := im.Bounds()
	if maxDim > 0 && (bounds.Dx() > maxDim || bounds.Dy() > maxDim) {
		im = resize.Thumbnail(uint(maxDim), uint(maxDim), im, resize.Bilinear)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, im); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}
