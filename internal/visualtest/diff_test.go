//go:build visual

package visualtest_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/codecuttle/codecuttlectl/internal/visualtest"
)

// makeImage creates a solid-color test image.
func makeImage(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// makeImageWithStripe creates an image with a horizontal stripe at a given row.
func makeImageWithStripe(w, h, stripeY, stripeH int, bg, fg color.Color) *image.RGBA {
	img := makeImage(w, h, bg)
	for y := stripeY; y < stripeY+stripeH && y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fg)
		}
	}
	return img
}

func TestPixelDiff_Identical(t *testing.T) {
	img := makeImage(100, 100, color.White)
	diff, err := visualtest.PixelDiff(img, img)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff != 0 {
		t.Errorf("identical images should have 0 diff, got %f", diff)
	}
}

func TestPixelDiff_Completely_Different(t *testing.T) {
	a := makeImage(100, 100, color.White)
	b := makeImage(100, 100, color.Black)
	diff, err := visualtest.PixelDiff(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff != 1.0 {
		t.Errorf("completely different images should have 1.0 diff, got %f", diff)
	}
}

func TestPixelDiff_Partial(t *testing.T) {
	a := makeImage(100, 100, color.White)
	b := makeImage(100, 100, color.White)
	// Change the top 10 rows (10% of pixels)
	for y := 0; y < 10; y++ {
		for x := 0; x < 100; x++ {
			b.Set(x, y, color.Black)
		}
	}
	diff, err := visualtest.PixelDiff(a, b)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if diff < 0.09 || diff > 0.11 {
		t.Errorf("expected ~10%% diff, got %f", diff)
	}
}

func TestPixelDiff_SizeMismatch(t *testing.T) {
	a := makeImage(100, 100, color.White)
	b := makeImage(200, 100, color.White)
	_, err := visualtest.PixelDiff(a, b)
	if err == nil {
		t.Error("expected error for mismatched dimensions")
	}
}

func TestDetectViewportJump_Stable(t *testing.T) {
	// 5 identical frames — no jumps
	img := makeImage(100, 100, color.White)
	frames := []image.Image{img, img, img, img, img}

	jumps, err := visualtest.DetectViewportJump(frames, 0.02, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jumps) != 0 {
		t.Errorf("expected 0 jumps, got %d", len(jumps))
	}
}

func TestDetectViewportJump_WithJump(t *testing.T) {
	white := makeImage(100, 100, color.White)
	black := makeImage(100, 100, color.Black)
	// Sequence: stable, stable, JUMP, stable
	frames := []image.Image{white, white, white, black, black}

	jumps, err := visualtest.DetectViewportJump(frames, 0.02, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(jumps) != 1 {
		t.Errorf("expected 1 jump, got %d", len(jumps))
	}
	if len(jumps) > 0 && jumps[0].FromIndex != 2 {
		t.Errorf("expected jump at frame 2→3, got %d→%d", jumps[0].FromIndex, jumps[0].ToIndex)
	}
}

func TestMaxConsecutiveStable(t *testing.T) {
	white := makeImage(100, 100, color.White)
	black := makeImage(100, 100, color.Black)
	// 3 stable, 1 jump, 2 stable
	frames := []image.Image{white, white, white, black, black, black}

	maxStable, err := visualtest.MaxConsecutiveStable(frames, 0.02)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if maxStable != 3 {
		t.Errorf("expected max consecutive stable = 3, got %d", maxStable)
	}
}

func TestDiffMap(t *testing.T) {
	a := makeImage(100, 100, color.White)
	b := makeImage(100, 100, color.White)
	// Change one pixel
	b.Set(50, 50, color.Black)

	diffImg := visualtest.DiffMap(a, b)
	// The changed pixel should be red
	r, g, _, _ := diffImg.At(50, 50).RGBA()
	if r>>8 != 255 || g != 0 {
		t.Errorf("expected red pixel at (50,50), got RGBA %d,%d,...", r>>8, g>>8)
	}
	// An unchanged pixel should be black
	r, g, _, _ = diffImg.At(0, 0).RGBA()
	if r != 0 || g != 0 {
		t.Errorf("expected black pixel at (0,0), got RGBA %d,%d,...", r>>8, g>>8)
	}
}

func TestScrollDirection_NoScroll(t *testing.T) {
	img := makeImage(100, 100, color.White)
	dir := visualtest.ScrollDirection(img, img, 20)
	if dir != 0 {
		t.Errorf("expected 0 (no scroll), got %d", dir)
	}
}

func TestScrollDirection_Down(t *testing.T) {
	// Frame A has a stripe at row 20, Frame B has it at row 40 (scrolled down)
	a := makeImageWithStripe(100, 200, 20, 20, color.White, color.Black)
	b := makeImageWithStripe(100, 200, 40, 20, color.White, color.Black)

	dir := visualtest.ScrollDirection(a, b, 20)
	if dir <= 0 {
		t.Errorf("expected positive (downward scroll), got %d", dir)
	}
}

func TestIsMonotonicallyScrolling_Stable(t *testing.T) {
	img := makeImage(100, 100, color.White)
	frames := []image.Image{img, img, img}

	monotonic, _ := visualtest.IsMonotonicallyScrolling(frames, 20)
	if !monotonic {
		t.Error("expected monotonic (no movement = stable)")
	}
}

func TestPixelDiffRegion(t *testing.T) {
	a := makeImage(100, 100, color.White)
	b := makeImage(100, 100, color.White)
	// Change only pixels in a 10x10 region at (50,50)
	for y := 50; y < 60; y++ {
		for x := 50; x < 60; x++ {
			b.Set(x, y, color.Black)
		}
	}

	// Full diff should be 1%
	fullDiff, _ := visualtest.PixelDiff(a, b)
	if fullDiff < 0.005 || fullDiff > 0.015 {
		t.Errorf("expected ~1%% full diff, got %f", fullDiff)
	}

	// Region diff of the changed area should be 100%
	region := image.Rect(50, 50, 60, 60)
	regionDiff, _ := visualtest.PixelDiffRegion(a, b, region)
	if regionDiff != 1.0 {
		t.Errorf("expected 100%% region diff, got %f", regionDiff)
	}

	// Region diff of an unchanged area should be 0%
	region2 := image.Rect(0, 0, 10, 10)
	regionDiff2, _ := visualtest.PixelDiffRegion(a, b, region2)
	if regionDiff2 != 0 {
		t.Errorf("expected 0%% region diff for unchanged area, got %f", regionDiff2)
	}
}
