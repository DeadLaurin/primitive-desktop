# Primitive — Desktop GUI

A native macOS app for [Primitive](../README.md), built with
[Wails v2](https://wails.io) and the plain HTML/CSS/JS template (no bundler, no
npm install).

## Features

- **Native file chooser** and **drag-and-drop** for the target image, with a live
  thumbnail and dimensions.
- **Wide input format support** — everything the Go standard library reads
  (PNG, JPEG, GIF, BMP) plus, on macOS, HEIF/HEIC, AVIF, WebP, TIFF and several
  camera RAW formats, decoded through the system ImageIO framework.
- **Live preview** — the picture is streamed back as it is drawn, one shape at a
  time, so you can watch the image emerge.
- **Every CLI flag** is exposed: shape type, shape count, alpha, repeat, input
  size, output size, background colour and worker count.
- **Live stats**: shapes added, RMSE score, evaluations per second and elapsed time.
- **Stop** mid-run, keeping the result so far.
- **Save** as PNG, JPG, SVG or an **animated GIF** through a native save dialog.
- Keyboard: `Cmd+Enter` starts a run, `Esc` stops it.

## Animated GIF export

GIF uses a **built-in pure-Go encoder**, so it works with nothing installed. The
frames are rendered directly at the export size (the shape history is walked
once), and a single global colour table is computed across every frame so
colours stay stable instead of flickering.

Dithering is **off** by default. These images are made of flat shapes, where
Floyd-Steinberg error diffusion only adds visible grain and roughly doubles the
file size.

If ImageMagick is installed, it is detected at startup and offered as an
alternative encoder in the Export panel. Should it fail, the built-in encoder
takes over so an export always produces a file.

## Requirements

- macOS with Xcode command line tools (for cgo / WebKit).
- Go 1.26+.
- The Wails v2 CLI:

  ```bash
  go install github.com/wailsapp/wails/v2/cmd/wails@latest
  ```

  Make sure `$(go env GOPATH)/bin` is on your `PATH`.

## Build and run

```bash
cd desktop
wails build            # -> build/bin/Primitive.app
open build/bin/Primitive.app
```

For development with hot reload of the frontend:

```bash
wails dev
```

To try the backend without the GUI:

```bash
go test ./...
node frontend/test/ui_test.js
```

The first test runs the real algorithm pipeline (load → resize → score → save) and
the HEIC/HEIF decoding path. The second runs the real UI script against a stub DOM
and Wails bridge, checking that progress events populate every stat, that settings
are clamped before being sent to Go, that GIF export options are forwarded
correctly, and that the control states track the run.

## Layout

```
desktop/
├── main.go                     window options and asset embedding
├── app.go                      bound backend: PickInput, Start, Stop, Save, SaveGIF, Status
├── app_test.go                 headless tests of the algorithm path
├── loadimage.go                format-agnostic image loading
├── imagedecode_darwin.go       HEIF/HEIC and friends via macOS ImageIO (cgo)
├── imagedecode_other.go        fallback for platforms without ImageIO
├── wails.json                  Wails project config
├── frontend/
│   ├── src/                    the embedded UI (index.html, main.css, main.js)
│   ├── test/ui_test.js         headless DOM test for main.js
│   └── wailsjs/                generated JS bindings (do not edit)
└── build/
    ├── appicon.png             app icon source
    ├── appicon.py              regenerates appicon.png
    └── bin/Primitive.app       build output
```

## How it is wired

`app.go` binds an `App` struct to the frontend. Wails exposes each exported
method as `window.go.main.App.<Method>()` in JavaScript, returning a Promise,
and `window.runtime.EventsOn/EventsEmit` carries events the other way.

The algorithm runs on a background goroutine. `App.mu` guards the model, and the
lock is held across each `model.Step` call *and* the preview encode, so the model
is never read while a shape is being added. Progress is emitted as `run:progress`
(one event per shape) with preview frames throttled to roughly 10 per second;
`run:done` fires when the run finishes or is stopped.

Preview images are PNG `data:` URLs, downscaled to 512 px. Flat-coloured
primitive output compresses extremely well, so this stays cheap to transport.
Saving always encodes at full output resolution.

`loadimage.go` tries the Go standard library first and only falls back to
`platformDecode` when that fails, so ordinary PNG/JPEG loads never touch cgo.
The ImageIO decoder is behind a `darwin && cgo` build tag; without cgo the app
still builds and simply supports the standard formats.
