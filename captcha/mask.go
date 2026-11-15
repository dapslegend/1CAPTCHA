package captcha

import (
	"image"
	"math"
)

// puzzleMask rasterises the outline of a puzzle piece: a square body, a round
// knob protruding from the top edge and a round notch cut out of the right
// edge. The result is an alpha mask the size of the piece's bounding box, i.e.
// the body padded by knob pixels on every side. Coverage is supersampled to
// keep the edges smooth.
func puzzleMask(bodyW, bodyH, knob int) *image.Alpha {
	const samples = 4

	boxW, boxH := bodyW+2*knob, bodyH+2*knob
	mask := image.NewAlpha(image.Rect(0, 0, boxW, boxH))

	bw, bh, r := float64(bodyW), float64(bodyH), float64(knob)
	knobCX, knobCY := bw/2, 0.0  // relative to the body's top-left corner
	notchCX, notchCY := bw, bh/2 // relative to the body's top-left corner
	notchR := r * 0.72

	inside := func(x, y float64) bool {
		cx, cy := x-r, y-r // body-local coordinates
		inBody := cx >= 0 && cx <= bw && cy >= 0 && cy <= bh
		inKnob := math.Hypot(cx-knobCX, cy-knobCY) <= r
		if !inBody && !inKnob {
			return false
		}
		return math.Hypot(cx-notchCX, cy-notchCY) > notchR
	}

	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			hits := 0
			for sy := 0; sy < samples; sy++ {
				for sx := 0; sx < samples; sx++ {
					fx := float64(x) + (float64(sx)+0.5)/samples
					fy := float64(y) + (float64(sy)+0.5)/samples
					if inside(fx, fy) {
						hits++
					}
				}
			}
			if hits > 0 {
				mask.Pix[mask.PixOffset(x, y)] = uint8(255 * hits / (samples * samples))
			}
		}
	}
	return mask
}
