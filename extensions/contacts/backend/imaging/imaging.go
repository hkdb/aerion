// Package imaging provides minimal image decode/resize/encode helpers used by
// the Contacts extension's photo pipeline.
//
// The intended use case is contact-photo handling (~256x256 max edge) where
// inline base64 in vCards needs to stay compact. Not a general image-processing
// package — we intentionally do JPEG-only output to keep the contract simple.
package imaging

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // register decoders for the formats we accept
	"image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp" // WEBP decoder

	"golang.org/x/image/draw"
)

// MaxAcceptedBytes is a defensive cap on the raw input size we'll attempt to
// decode. Real photos from a phone camera land in the 1-10 MB range; anything
// over this is almost certainly something we shouldn't be loading into memory.
// Callers can enforce their own smaller limits on top.
const MaxAcceptedBytes = 32 * 1024 * 1024 // 32 MB

// ResizeOptions parameterizes ResizeToJPEG. Zero values are sensible defaults.
type ResizeOptions struct {
	// MaxEdge caps both width and height. Aspect ratio is preserved. 0 = 256.
	MaxEdge int
	// Quality is the JPEG quality (1-100). 0 = 85.
	Quality int
}

// ResizeToJPEG decodes the given image bytes (PNG / JPEG / WEBP / GIF),
// rescales so neither dimension exceeds MaxEdge while preserving aspect
// ratio, and re-encodes as JPEG at the given quality. Returns the encoded
// bytes plus "image/jpeg" as the media type so callers can hand both to
// downstream code (e.g., a vCard PHOTO line) without re-checking format.
//
// Uses golang.org/x/image/draw.CatmullRom for the rescale — high quality
// for our typical small-photo case, fast enough for synchronous use during
// a file pick.
func ResizeToJPEG(raw []byte, opts ResizeOptions) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("imaging: empty input")
	}
	if len(raw) > MaxAcceptedBytes {
		return nil, "", fmt.Errorf("imaging: input exceeds %d bytes", MaxAcceptedBytes)
	}
	maxEdge := opts.MaxEdge
	if maxEdge <= 0 {
		maxEdge = 256
	}
	quality := opts.Quality
	if quality <= 0 {
		quality = 85
	}

	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("imaging: decode: %w", err)
	}

	srcBounds := src.Bounds()
	srcW := srcBounds.Dx()
	srcH := srcBounds.Dy()
	if srcW <= 0 || srcH <= 0 {
		return nil, "", fmt.Errorf("imaging: zero-size image")
	}

	// Compute target size preserving aspect ratio. If the image is already
	// within the max edge, copy through without rescale (preserves quality).
	scale := 1.0
	if longest := srcW; srcH > longest {
		longest = srcH
		if longest > maxEdge {
			scale = float64(maxEdge) / float64(longest)
		}
	} else if srcW > maxEdge {
		scale = float64(maxEdge) / float64(srcW)
	}

	dstW := int(float64(srcW)*scale + 0.5)
	dstH := int(float64(srcH)*scale + 0.5)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	var out bytes.Buffer
	if err := jpeg.Encode(&out, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, "", fmt.Errorf("imaging: encode jpeg: %w", err)
	}
	return out.Bytes(), "image/jpeg", nil
}

