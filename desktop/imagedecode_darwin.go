//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ImageIO -framework CoreFoundation

#include <stdlib.h>
#include <stdio.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ImageIO/ImageIO.h>

typedef struct {
	unsigned char *pixels;
	size_t width;
	size_t height;
	char err[256];
} dsh_decoded;

// dsh_decode_image decodes any image format the system understands (HEIF,
// HEIC, AVIF, WebP, TIFF, RAW, ...) into a tightly packed RGBA buffer.
// Returns 1 on success, 0 on failure with the reason in out->err.
static int dsh_decode_image(const void *bytes, size_t len, dsh_decoded *out) {
	out->pixels = NULL;
	out->width = 0;
	out->height = 0;
	out->err[0] = '\0';

	CFDataRef data = CFDataCreate(kCFAllocatorDefault, (const UInt8 *)bytes, (CFIndex)len);
	if (data == NULL) {
		snprintf(out->err, sizeof(out->err), "could not copy image data");
		return 0;
	}

	CGImageSourceRef source = CGImageSourceCreateWithData(data, NULL);
	CFRelease(data);
	if (source == NULL) {
		snprintf(out->err, sizeof(out->err), "unrecognised image format");
		return 0;
	}

	CGImageRef image = CGImageSourceCreateImageAtIndex(source, 0, NULL);
	if (image == NULL) {
		// Some formats only expose a thumbnail unless full decoding is allowed.
		CFDictionaryRef opts = CFDictionaryCreate(
			kCFAllocatorDefault,
			(const void **)&kCGImageSourceShouldCacheImmediately,
			(const void **)&kCFBooleanTrue, 1,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		if (opts != NULL) {
			image = CGImageSourceCreateImageAtIndex(source, 0, opts);
			CFRelease(opts);
		}
	}
	CFRelease(source);
	if (image == NULL) {
		snprintf(out->err, sizeof(out->err), "could not decode image data");
		return 0;
	}

	size_t width = CGImageGetWidth(image);
	size_t height = CGImageGetHeight(image);
	if (width == 0 || height == 0) {
		CGImageRelease(image);
		snprintf(out->err, sizeof(out->err), "image has zero size");
		return 0;
	}

	size_t stride = width * 4;
	unsigned char *pixels = (unsigned char *)malloc(stride * height);
	if (pixels == NULL) {
		CGImageRelease(image);
		snprintf(out->err, sizeof(out->err), "out of memory for %zux%zu image", width, height);
		return 0;
	}

	CGColorSpaceRef space = CGColorSpaceCreateDeviceRGB();
	CGContextRef ctx = CGBitmapContextCreate(
		pixels, width, height, 8, stride, space,
		kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
	CGColorSpaceRelease(space);
	if (ctx == NULL) {
		free(pixels);
		CGImageRelease(image);
		snprintf(out->err, sizeof(out->err), "could not create a bitmap context");
		return 0;
	}

	CGContextDrawImage(ctx, CGRectMake(0, 0, (CGFloat)width, (CGFloat)height), image);
	CGContextRelease(ctx);
	CGImageRelease(image);

	out->pixels = pixels;
	out->width = width;
	out->height = height;
	return 1;
}
*/
import "C"

import (
	"errors"
	"image"
	"unsafe"
)

// platformDecode handles formats the Go standard library cannot, using the
// system image framework. On macOS that covers HEIF/HEIC, AVIF, WebP, TIFF and
// several camera RAW formats.
func platformDecode(data []byte) (image.Image, error) {
	if len(data) == 0 {
		return nil, errors.New("empty image data")
	}

	var out C.dsh_decoded
	if C.dsh_decode_image(unsafe.Pointer(&data[0]), C.size_t(len(data)), &out) == 0 {
		return nil, errors.New(C.GoString(&out.err[0]))
	}
	defer C.free(unsafe.Pointer(out.pixels))

	width, height := int(out.width), int(out.height)
	if width <= 0 || height <= 0 {
		return nil, errors.New("decoded image has zero size")
	}

	// ImageIO gave us premultiplied RGBA; convert it to straight NRGBA.
	n := width * height * 4
	raw := unsafe.Slice((*byte)(unsafe.Pointer(out.pixels)), n)
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < n; i += 4 {
		a := raw[i+3]
		switch a {
		case 0:
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0, 0, 0, 0
		case 255:
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] =
				raw[i], raw[i+1], raw[i+2], 255
		default:
			// A premultiplied component never exceeds alpha, so this stays in range.
			pa := uint32(a)
			img.Pix[i] = uint8(uint32(raw[i]) * 255 / pa)
			img.Pix[i+1] = uint8(uint32(raw[i+1]) * 255 / pa)
			img.Pix[i+2] = uint8(uint32(raw[i+2]) * 255 / pa)
			img.Pix[i+3] = a
		}
	}
	return img, nil
}
