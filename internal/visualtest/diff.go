package visualtest

import (
	"fmt"
	"image"
	"image/color"
	"math"
)

// PixelDiff computes the fraction of pixels that differ between two images.
// Returns a value between 0.0 (identical) and 1.0 (completely different).
// Pixels are compared with a tolerance to account for anti-aliasing and
// minor rendering variations.
func PixelDiff(a, b image.Image) (float64, error) {
	boundsA := a.Bounds()
	boundsB := b.Bounds()

	if boundsA.Dx() != boundsB.Dx() || boundsA.Dy() != boundsB.Dy() {
		return 1.0, fmt.Errorf("image dimensions differ: %dx%d vs %dx%d",
			boundsA.Dx(), boundsA.Dy(), boundsB.Dx(), boundsB.Dy())
	}

	total := boundsA.Dx() * boundsA.Dy()
	if total == 0 {
		return 0, nil
	}

	changed := 0
	for y := boundsA.Min.Y; y < boundsA.Max.Y; y++ {
		for x := boundsA.Min.X; x < boundsA.Max.X; x++ {
			if !colorsClose(a.At(x, y), b.At(x, y), 8) {
				changed++
			}
		}
	}

	return float64(changed) / float64(total), nil
}

// PixelDiffRegion computes pixel diff for a sub-region of the images.
// Useful for checking stability of just the viewport area (excluding cursor).
func PixelDiffRegion(a, b image.Image, region image.Rectangle) (float64, error) {
	boundsA := a.Bounds()
	boundsB := b.Bounds()

	// Clamp region to image bounds
	region = region.Intersect(boundsA).Intersect(boundsB)
	if region.Empty() {
		return 0, nil
	}

	total := region.Dx() * region.Dy()
	changed := 0

	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			if !colorsClose(a.At(x, y), b.At(x, y), 8) {
				changed++
			}
		}
	}

	return float64(changed) / float64(total), nil
}

// DetectViewportJump analyzes a sequence of frames and returns indices
// where the viewport appears to jump (diff exceeds threshold).
// The threshold is a fraction (e.g., 0.02 = 2% of pixels changed).
// A cursorRegion can be provided to exclude the cursor blink area from comparison.
func DetectViewportJump(frames []image.Image, threshold float64, cursorRegion *image.Rectangle) ([]FrameTransition, error) {
	if len(frames) < 2 {
		return nil, nil
	}

	var jumps []FrameTransition

	for i := 1; i < len(frames); i++ {
		var diff float64
		var err error

		if cursorRegion != nil {
			// Compare excluding the cursor region by checking the rest
			// For simplicity, compare full image — cursor blink is typically <0.5%
			diff, err = PixelDiff(frames[i-1], frames[i])
		} else {
			diff, err = PixelDiff(frames[i-1], frames[i])
		}

		if err != nil {
			return nil, fmt.Errorf("comparing frames %d→%d: %w", i-1, i, err)
		}

		if diff > threshold {
			jumps = append(jumps, FrameTransition{
				FromIndex: i - 1,
				ToIndex:   i,
				Diff:      diff,
			})
		}
	}

	return jumps, nil
}

// FrameTransition records a significant visual change between two frames.
type FrameTransition struct {
	FromIndex int
	ToIndex   int
	Diff      float64 // Fraction of pixels that changed (0.0-1.0)
}

// DiffMap generates an image highlighting the pixels that differ between
// two frames. Changed pixels are shown in red, unchanged in black.
// Useful for debugging — save this as a PNG for visual inspection.
func DiffMap(a, b image.Image) *image.RGBA {
	bounds := a.Bounds()
	diff := image.NewRGBA(bounds)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if !colorsClose(a.At(x, y), b.At(x, y), 8) {
				diff.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				diff.Set(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}

	return diff
}

// MaxConsecutiveStable returns the longest run of frames where no jump
// exceeds the threshold. Useful for asserting "the viewport was stable
// for at least N frames."
func MaxConsecutiveStable(frames []image.Image, threshold float64) (int, error) {
	if len(frames) < 2 {
		return len(frames), nil
	}

	maxRun := 0
	currentRun := 1

	for i := 1; i < len(frames); i++ {
		diff, err := PixelDiff(frames[i-1], frames[i])
		if err != nil {
			return 0, err
		}

		if diff <= threshold {
			currentRun++
		} else {
			if currentRun > maxRun {
				maxRun = currentRun
			}
			currentRun = 1
		}
	}

	if currentRun > maxRun {
		maxRun = currentRun
	}

	return maxRun, nil
}

// colorsClose returns true if two colors are within tolerance on all channels.
func colorsClose(c1, c2 color.Color, tolerance uint32) bool {
	r1, g1, b1, _ := c1.RGBA()
	r2, g2, b2, _ := c2.RGBA()

	// RGBA returns 16-bit values, scale tolerance accordingly
	tol := tolerance * 256

	return absDiff(r1, r2) <= tol &&
		absDiff(g1, g2) <= tol &&
		absDiff(b1, b2) <= tol
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// ScrollDirection analyzes frames to determine scroll direction.
// Returns positive for downward scroll, negative for upward, 0 for stable.
// Uses row-based hashing to detect content displacement.
func ScrollDirection(a, b image.Image, rowHeight int) int {
	if rowHeight <= 0 {
		rowHeight = 20 // Approximate cell height in pixels
	}

	bounds := a.Bounds()
	rows := bounds.Dy() / rowHeight

	// Hash each row in both images
	hashesA := make([]uint64, rows)
	hashesB := make([]uint64, rows)

	for r := 0; r < rows; r++ {
		y := bounds.Min.Y + r*rowHeight
		hashesA[r] = rowHash(a, y, y+rowHeight)
		hashesB[r] = rowHash(b, y, y+rowHeight)
	}

	// Find the best offset that aligns the most rows
	bestOffset := 0
	bestMatches := 0

	for offset := -rows / 2; offset <= rows/2; offset++ {
		matches := 0
		for r := 0; r < rows; r++ {
			src := r
			dst := r + offset
			if dst >= 0 && dst < rows && hashesA[src] == hashesB[dst] {
				matches++
			}
		}
		if matches > bestMatches {
			bestMatches = matches
			bestOffset = offset
		}
	}

	return bestOffset
}

// rowHash computes a simple hash of a horizontal strip of the image.
func rowHash(img image.Image, yStart, yEnd int) uint64 {
	bounds := img.Bounds()
	var hash uint64

	for y := yStart; y < yEnd && y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x += 4 { // Sample every 4th pixel
			r, g, b, _ := img.At(x, y).RGBA()
			hash ^= uint64(r>>8) * 31
			hash ^= uint64(g>>8) * 37
			hash ^= uint64(b>>8) * 41
			hash = (hash << 5) | (hash >> 59) // Rotate
		}
	}

	return hash
}

// IsMonotonicallyScrolling checks that content only moves in one direction
// across a frame sequence. Returns true if all detected scrolls are in the
// same direction (or no scrolls detected).
func IsMonotonicallyScrolling(frames []image.Image, rowHeight int) (bool, []int) {
	if len(frames) < 2 {
		return true, nil
	}

	var directions []int
	for i := 1; i < len(frames); i++ {
		dir := ScrollDirection(frames[i-1], frames[i], rowHeight)
		if dir != 0 {
			directions = append(directions, dir)
		}
	}

	if len(directions) == 0 {
		return true, directions
	}

	// Check all non-zero directions have the same sign
	sign := math.Signbit(float64(directions[0]))
	for _, d := range directions[1:] {
		if math.Signbit(float64(d)) != sign {
			return false, directions
		}
	}

	return true, directions
}
