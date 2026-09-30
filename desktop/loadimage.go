package main

import (
	"bytes"
	"fmt"
	"image"
	"os"
	"path/filepath"

	// Register the decoders the standard library ships with.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// imageFileFilter lists the extensions offered in the open dialog. HEIF, HEIC
// and the other platform formats are handled by platformDecode.
const imageFileFilter = "*.png;*.jpg;*.jpeg;*.gif;*.bmp;*.tif;*.tiff;*.webp;*.heic;*.heif;*.avif"

// loadImage decodes an image file.
//
// The Go standard library is tried first; anything it cannot handle falls back
// to the platform decoder, which on macOS covers HEIF/HEIC, AVIF, WebP, TIFF
// and several camera RAW formats.
func loadImage(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	img, _, stdErr := image.Decode(bytes.NewReader(data))
	if stdErr == nil {
		return img, nil
	}

	if img, platformErr := platformDecode(data); platformErr == nil {
		return img, nil
	}

	// Report the standard library's error; it is usually the clearer one.
	return nil, fmt.Errorf("could not decode %s: %v", filepath.Base(path), stdErr)
}
