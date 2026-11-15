package captcha

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	mrand "math/rand"
)

// artwork is one rendered challenge: the encoded background with the sockets
// shaded in, the encoded piece, and the secret answer.
type artwork struct {
	background []byte
	piece      []byte
	answerX    int
	answerY    int
	decoyX     int
	decoyY     int
	hasDecoy   bool
}

func render(cfg Config, rnd *mrand.Rand) (*artwork, error) {
	bg := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	painters[rnd.Intn(len(painters))](bg, rnd)
	grainAndVignette(bg, rnd)

	answerX, answerY, decoyX, decoyY, hasDecoy := chooseSockets(bg, cfg, rnd)

	mask := puzzleMask(cfg.PieceWidth, cfg.PieceHeight, cfg.Knob)
	piece := cutPiece(bg, mask, answerX, answerY)
	shadeSocket(bg, mask, answerX, answerY)
	if hasDecoy {
		shadeSocket(bg, mask, decoyX, decoyY)
	}

	var bgBuf bytes.Buffer
	if err := jpeg.Encode(&bgBuf, bg, &jpeg.Options{Quality: 90}); err != nil {
		return nil, err
	}
	var pieceBuf bytes.Buffer
	if err := png.Encode(&pieceBuf, piece); err != nil {
		return nil, err
	}

	return &artwork{
		background: bgBuf.Bytes(),
		piece:      pieceBuf.Bytes(),
		answerX:    answerX,
		answerY:    answerY,
		decoyX:     decoyX,
		decoyY:     decoyY,
		hasDecoy:   hasDecoy,
	}, nil
}

// chooseSockets places the real socket anywhere in the free band and, when
// decoys are enabled, tries to place a second identical socket far enough away
// and in a region whose colours differ clearly, so a human can tell which
// socket the piece was cut from by edge continuity.
func chooseSockets(bg *image.RGBA, cfg Config, rnd *mrand.Rand) (int, int, int, int, bool) {
	boxW, boxH := cfg.PieceWidth+2*cfg.Knob, cfg.PieceHeight+2*cfg.Knob
	minX, maxX := boxW+20, cfg.Width-boxW-4
	minY, maxY := 4, cfg.Height-boxH-8

	answerX := minX + rnd.Intn(maxX-minX+1)
	answerY := minY + rnd.Intn(maxY-minY+1)
	if cfg.Decoys <= 0 {
		return answerX, answerY, 0, 0, false
	}

	answerColor := regionColor(bg, answerX, answerY, boxW, boxH)
	bestX, bestY, bestDist := 0, 0, 0.0
	for attempt := 0; attempt < 48; attempt++ {
		dx := minX + rnd.Intn(maxX-minX+1)
		dy := minY + rnd.Intn(maxY-minY+1)
		if abs(answerX-dx) < boxW+12 && abs(answerY-dy) < boxH+12 {
			continue
		}
		dist := colorDist(answerColor, regionColor(bg, dx, dy, boxW, boxH))
		if dist >= 55 {
			return answerX, answerY, dx, dy, true
		}
		if dist > bestDist {
			bestX, bestY, bestDist = dx, dy, dist
		}
	}
	// Smooth themes (aurora gradients) rarely offer a strongly contrasting
	// region; keep the most contrasting candidate instead of dropping the
	// decoy, as long as the tint difference stays perceptible.
	if bestDist >= 28 {
		return answerX, answerY, bestX, bestY, true
	}
	return answerX, answerY, 0, 0, false
}

func cutPiece(bg *image.RGBA, mask *image.Alpha, atX, atY int) *image.NRGBA {
	boxW, boxH := mask.Bounds().Dx(), mask.Bounds().Dy()
	piece := image.NewNRGBA(image.Rect(0, 0, boxW, boxH))
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			alpha := mask.Pix[mask.PixOffset(x, y)]
			if alpha == 0 {
				continue
			}
			i := bg.PixOffset(atX+x, atY+y)
			piece.SetNRGBA(x, y, color.NRGBA{R: bg.Pix[i], G: bg.Pix[i+1], B: bg.Pix[i+2], A: alpha})
		}
	}
	return piece
}

// shadeSocket darkens the area the piece was cut from so humans see a hole.
func shadeSocket(bg *image.RGBA, mask *image.Alpha, atX, atY int) {
	boxW, boxH := mask.Bounds().Dx(), mask.Bounds().Dy()
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			f := float64(mask.Pix[mask.PixOffset(x, y)]) / 255
			if f == 0 {
				continue
			}
			i := bg.PixOffset(atX+x, atY+y)
			bg.Pix[i] = clamp8(float64(bg.Pix[i])*(1-0.72*f) + 8*f)
			bg.Pix[i+1] = clamp8(float64(bg.Pix[i+1])*(1-0.70*f) + 12*f)
			bg.Pix[i+2] = clamp8(float64(bg.Pix[i+2])*(1-0.60*f) + 26*f)
		}
	}
}

func regionColor(bg *image.RGBA, x, y, w, h int) [3]float64 {
	var sum [3]float64
	n := 0
	for yy := y; yy < y+h; yy += 2 {
		for xx := x; xx < x+w; xx += 2 {
			i := bg.PixOffset(xx, yy)
			sum[0] += float64(bg.Pix[i])
			sum[1] += float64(bg.Pix[i+1])
			sum[2] += float64(bg.Pix[i+2])
			n++
		}
	}
	return [3]float64{sum[0] / float64(n), sum[1] / float64(n), sum[2] / float64(n)}
}

func colorDist(a, b [3]float64) float64 {
	dr, dg, db := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

// painters are the visual themes. A theme is picked at random per challenge so
// the captcha never repeats the same look. All themes stay on the light side so
// the dark sockets remain obvious.
var painters = []func(*image.RGBA, *mrand.Rand){
	paintAurora, paintWaves, paintMemphis, paintTerrazzo, paintMesh, paintTopo, paintBauhaus,
}

func paintAurora(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()

	hue := rnd.Float64() * 360
	from := hsv(hue, 0.38+rnd.Float64()*0.22, 0.78+rnd.Float64()*0.14)
	to := hsv(math.Mod(hue+70+rnd.Float64()*140, 360), 0.42+rnd.Float64()*0.24, 0.62+rnd.Float64()*0.18)

	angle := rnd.Float64() * 2 * math.Pi
	dx, dy := math.Cos(angle), math.Sin(angle)
	proj := [4]float64{0, float64(w) * dx, float64(h) * dy, float64(w)*dx + float64(h)*dy}
	lo, hi := proj[0], proj[0]
	for _, p := range proj {
		lo = math.Min(lo, p)
		hi = math.Max(hi, p)
	}
	span := math.Max(hi-lo, 1e-6)

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			t := smoothstep(clamp01((float64(x)*dx + float64(y)*dy - lo) / span))
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8((from[0]*(1-t) + to[0]*t) * 255)
			img.Pix[i+1] = clamp8((from[1]*(1-t) + to[1]*t) * 255)
			img.Pix[i+2] = clamp8((from[2]*(1-t) + to[2]*t) * 255)
			img.Pix[i+3] = 255
		}
	}

	for i := 0; i < 24; i++ {
		cx := rnd.Float64() * float64(w)
		cy := rnd.Float64() * float64(h)
		rx := 10 + rnd.Float64()*80
		ry := rx * (0.35 + rnd.Float64()*1.1)
		tint := hsv(math.Mod(hue+rnd.Float64()*300-150, 360), 0.25+rnd.Float64()*0.40, 0.65+rnd.Float64()*0.35)
		drawBlob(img, cx, cy, rx, ry, 0.07+rnd.Float64()*0.15, tint)
	}
	for i := 0; i < 3; i++ {
		drawRing(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			20+rnd.Float64()*80, 1.5+rnd.Float64()*4.5, 0.05+rnd.Float64()*0.08, [3]float64{1, 1, 1})
	}
	for i := 0; i < 10; i++ {
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			1+rnd.Float64()*2.5, 1+rnd.Float64()*2.5, 0.20+rnd.Float64()*0.18, [3]float64{1, 1, 1})
	}
}

func paintWaves(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360

	top := hsv(hue, 0.30+rnd.Float64()*0.15, 0.90+rnd.Float64()*0.06)
	bottom := hsv(math.Mod(hue+50+rnd.Float64()*80, 360), 0.30+rnd.Float64()*0.20, 0.72+rnd.Float64()*0.12)
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8((top[0]*(1-t) + bottom[0]*t) * 255)
			img.Pix[i+1] = clamp8((top[1]*(1-t) + bottom[1]*t) * 255)
			img.Pix[i+2] = clamp8((top[2]*(1-t) + bottom[2]*t) * 255)
			img.Pix[i+3] = 255
		}
	}

	layers := 5
	for l := 0; l < layers; l++ {
		base := float64(h)*(0.22+0.68*float64(l)/float64(layers-1)) + (rnd.Float64()-0.5)*16
		amp := 5 + rnd.Float64()*16
		freq := 1.2 + rnd.Float64()*2.4
		phase := rnd.Float64() * 2 * math.Pi
		col := hsv(math.Mod(hue+float64(l)*36+rnd.Float64()*50-25, 360), 0.30+rnd.Float64()*0.20, 0.66+rnd.Float64()*0.26)
		alpha := 0.45 + rnd.Float64()*0.35

		for x := 0; x < w; x++ {
			u := 2 * math.Pi * freq * float64(x) / float64(w)
			line := base + amp*math.Sin(u+phase) + amp*0.35*math.Sin(2.7*u+phase*1.7)
			y0 := int(math.Ceil(line))
			edge := clamp01(float64(y0) - line)
			for y := y0; y < h; y++ {
				a := alpha
				if y == y0 {
					a *= edge
				}
				i := img.PixOffset(x, y)
				img.Pix[i] = clamp8(float64(img.Pix[i])*(1-a) + col[0]*255*a)
				img.Pix[i+1] = clamp8(float64(img.Pix[i+1])*(1-a) + col[1]*255*a)
				img.Pix[i+2] = clamp8(float64(img.Pix[i+2])*(1-a) + col[2]*255*a)
			}
		}
	}
}

func paintMemphis(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360
	base := hsv(hue, 0.14+rnd.Float64()*0.10, 0.92+rnd.Float64()*0.05)
	fillFlat(img, base)

	for i := 0; i < 16; i++ {
		col := hsv(math.Mod(hue+rnd.Float64()*320-160, 360), 0.35+rnd.Float64()*0.20, 0.78+rnd.Float64()*0.17)
		cx, cy := rnd.Float64()*float64(w), rnd.Float64()*float64(h)
		size := 14 + rnd.Float64()*46
		rot := rnd.Float64() * math.Pi
		alpha := 0.55 + rnd.Float64()*0.40

		switch rnd.Intn(4) {
		case 0:
			stamp(img, cx, cy, size, func(x, y float64) bool { return math.Hypot(x, y) <= size }, col, alpha)
		case 1:
			stamp(img, cx, cy, size, func(x, y float64) bool { return math.Abs(math.Hypot(x, y)-size*0.8) <= size*0.14 }, col, alpha)
		case 2:
			stamp(img, cx, cy, size, func(x, y float64) bool {
				px, py := rotate(x, y, -rot)
				return py > -size*0.7 && py < size*0.7*math.Cos(px/size*1.2) && math.Abs(px) < size*0.8
			}, col, alpha)
		default:
			stamp(img, cx, cy, size, func(x, y float64) bool {
				px, py := rotate(x, y, -rot)
				return math.Abs(px) <= size*0.7 && math.Abs(py) <= size*0.7
			}, col, alpha)
		}
	}
	for i := 0; i < 40; i++ {
		stamp(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h), 1.5+rnd.Float64()*2,
			func(x, y float64) bool { return math.Hypot(x, y) <= 2 }, [3]float64{1, 1, 1}, 0.5)
	}
}

func paintTerrazzo(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360
	fillFlat(img, hsv(hue, 0.10+rnd.Float64()*0.08, 0.93+rnd.Float64()*0.04))

	for i := 0; i < 6; i++ {
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			40+rnd.Float64()*80, 40+rnd.Float64()*80, 0.06+rnd.Float64()*0.06,
			hsv(math.Mod(hue+rnd.Float64()*120-60, 360), 0.20+rnd.Float64()*0.15, 0.80+rnd.Float64()*0.15))
	}
	for i := 0; i < 70; i++ {
		col := hsv(rnd.Float64()*360, 0.20+rnd.Float64()*0.25, 0.65+rnd.Float64()*0.28)
		size := 3 + rnd.Float64()*9
		rot := rnd.Float64() * math.Pi
		squash := 0.4 + rnd.Float64()*0.8
		stamp(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h), size, func(x, y float64) bool {
			px, py := rotate(x, y, -rot)
			return (px*px)/(size*size)+(py*py)/((size*squash)*(size*squash)) <= 1
		}, col, 0.85)
	}
}

// paintMesh layers large soft radial colour fields over a quiet base — the
// "mesh gradient" look. The overlap regions are what make each render unique.
func paintMesh(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360
	fillFlat(img, hsv(hue, 0.08+rnd.Float64()*0.08, 0.94+rnd.Float64()*0.04))

	for i := 0; i < 9; i++ {
		col := hsv(math.Mod(hue+rnd.Float64()*300-150, 360), 0.38+rnd.Float64()*0.28, 0.72+rnd.Float64()*0.24)
		rx := 60 + rnd.Float64()*float64(w)*0.45
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h), rx, rx*(0.6+rnd.Float64()*0.7),
			0.22+rnd.Float64()*0.22, col)
	}
	for i := 0; i < 3; i++ {
		drawRing(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			30+rnd.Float64()*90, 1.5+rnd.Float64()*3.5, 0.10+rnd.Float64()*0.10, [3]float64{1, 1, 1})
	}
	for i := 0; i < 14; i++ {
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			1+rnd.Float64()*2.5, 1+rnd.Float64()*2.5, 0.25+rnd.Float64()*0.20, [3]float64{1, 1, 1})
	}
}

// paintTopo draws clusters of concentric contour rings, like a topographic
// map. Ring spacing and cluster hue drift keep every render distinct.
func paintTopo(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360
	fillFlat(img, hsv(math.Mod(hue+180, 360), 0.10+rnd.Float64()*0.08, 0.94+rnd.Float64()*0.04))

	for c := 0; c < 5; c++ {
		cx, cy := rnd.Float64()*float64(w), rnd.Float64()*float64(h)
		col := hsv(math.Mod(hue+rnd.Float64()*140-70, 360), 0.32+rnd.Float64()*0.26, 0.62+rnd.Float64()*0.28)
		rings := 3 + rnd.Intn(5)
		gap := 7 + rnd.Float64()*8
		for r := 0; r < rings; r++ {
			drawRing(img, cx, cy, gap*float64(r+1), 2+rnd.Float64()*2.5,
				0.30+rnd.Float64()*0.35, col)
		}
		if rnd.Intn(2) == 0 {
			drawBlob(img, cx, cy, gap*0.7, gap*0.7, 0.35+rnd.Float64()*0.3, col)
		}
	}
	for i := 0; i < 26; i++ {
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			1+rnd.Float64()*2, 1+rnd.Float64()*2, 0.30+rnd.Float64()*0.25, [3]float64{1, 1, 1})
	}
}

// paintBauhaus tiles the canvas with a strict grid of primary shapes — quarter
// circles, half discs, diagonals and dots — in a disciplined palette.
func paintBauhaus(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	hue := rnd.Float64() * 360
	fillFlat(img, hsv(hue, 0.10+rnd.Float64()*0.08, 0.93+rnd.Float64()*0.04))

	cell := 44 + rnd.Intn(18)
	for gy := 0; gy*cell < h+cell; gy++ {
		for gx := 0; gx*cell < w+cell; gx++ {
			if rnd.Intn(7) == 0 {
				continue // deliberate gaps keep the grid from feeling mechanical
			}
			cx := float64(gx*cell + cell/2)
			cy := float64(gy*cell + cell/2)
			s := float64(cell) * (0.42 + rnd.Float64()*0.10)
			col := hsv(math.Mod(hue+rnd.Float64()*200-100, 360), 0.34+rnd.Float64()*0.26, 0.70+rnd.Float64()*0.24)
			alpha := 0.75 + rnd.Float64()*0.25
			quadrant := rnd.Intn(4)

			switch rnd.Intn(4) {
			case 0: // quarter disc anchored in one corner of the cell
				stamp(img, cx, cy, s*1.6, func(x, y float64) bool {
					return math.Hypot(x, y) <= s && ((x >= 0) == (quadrant%2 == 0)) && ((y >= 0) == (quadrant/2 == 0))
				}, col, alpha)
			case 1: // half disc
				stamp(img, cx, cy, s*1.6, func(x, y float64) bool {
					return math.Hypot(x, y) <= s && (y >= 0) == (quadrant%2 == 0)
				}, col, alpha)
			case 2: // diagonal triangle
				stamp(img, cx, cy, s*1.6, func(x, y float64) bool {
					return math.Abs(x) <= s && math.Abs(y) <= s && math.Abs(x)+math.Abs(y) <= s*1.2
				}, col, alpha)
			default: // dot
				stamp(img, cx, cy, s*0.6, func(x, y float64) bool { return math.Hypot(x, y) <= s*0.55 }, col, alpha)
			}
		}
	}
	for i := 0; i < 20; i++ {
		drawBlob(img, rnd.Float64()*float64(w), rnd.Float64()*float64(h),
			1+rnd.Float64()*2, 1+rnd.Float64()*2, 0.30+rnd.Float64()*0.25, [3]float64{1, 1, 1})
	}
}

func fillFlat(img *image.RGBA, col [3]float64) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8(col[0] * 255)
			img.Pix[i+1] = clamp8(col[1] * 255)
			img.Pix[i+2] = clamp8(col[2] * 255)
			img.Pix[i+3] = 255
		}
	}
}

// stamp rasterises an arbitrary shape predicate centred at (cx, cy) with light
// supersampling; coordinates handed to pred are relative to the centre.
func stamp(img *image.RGBA, cx, cy, size float64, pred func(x, y float64) bool, col [3]float64, alpha float64) {
	b := img.Bounds()
	r := int(math.Ceil(size)) + 2
	minX, maxX := max(b.Min.X, int(cx)-r), min(b.Max.X-1, int(cx)+r)
	minY, maxY := max(b.Min.Y, int(cy)-r), min(b.Max.Y-1, int(cy)+r)

	const ss = 2
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			hits := 0
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					if pred(float64(x)+float64(sx)/ss-cx, float64(y)+float64(sy)/ss-cy) {
						hits++
					}
				}
			}
			if hits == 0 {
				continue
			}
			a := alpha * float64(hits) / float64(ss*ss)
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8(float64(img.Pix[i])*(1-a) + col[0]*255*a)
			img.Pix[i+1] = clamp8(float64(img.Pix[i+1])*(1-a) + col[1]*255*a)
			img.Pix[i+2] = clamp8(float64(img.Pix[i+2])*(1-a) + col[2]*255*a)
		}
	}
}

func rotate(x, y, angle float64) (float64, float64) {
	c, s := math.Cos(angle), math.Sin(angle)
	return x*c - y*s, x*s + y*c
}

func grainAndVignette(img *image.RGBA, rnd *mrand.Rand) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	midX, midY := float64(w)/2, float64(h)/2
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			n := (rnd.Float64() - 0.5) * 9
			nx := (float64(x) + 0.5 - midX) / midX
			ny := (float64(y) + 0.5 - midY) / midY
			vignette := 1 - 0.16*(nx*nx+ny*ny)
			img.Pix[i] = clamp8((float64(img.Pix[i]) + n) * vignette)
			img.Pix[i+1] = clamp8((float64(img.Pix[i+1]) + n) * vignette)
			img.Pix[i+2] = clamp8((float64(img.Pix[i+2]) + n) * vignette)
			img.Pix[i+3] = 255
		}
	}
}

func drawBlob(img *image.RGBA, cx, cy, rx, ry, alpha float64, col [3]float64) {
	bounds := img.Bounds()
	minX := max(bounds.Min.X, int(cx-rx)-1)
	maxX := min(bounds.Max.X-1, int(cx+rx)+1)
	minY := max(bounds.Min.Y, int(cy-ry)-1)
	maxY := min(bounds.Max.Y-1, int(cy+ry)+1)

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			dx := (float64(x) + 0.5 - cx) / rx
			dy := (float64(y) + 0.5 - cy) / ry
			d := dx*dx + dy*dy
			if d >= 1 {
				continue
			}
			a := alpha * smoothstep(1-d)
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8(float64(img.Pix[i])*(1-a) + col[0]*255*a)
			img.Pix[i+1] = clamp8(float64(img.Pix[i+1])*(1-a) + col[1]*255*a)
			img.Pix[i+2] = clamp8(float64(img.Pix[i+2])*(1-a) + col[2]*255*a)
		}
	}
}

func drawRing(img *image.RGBA, cx, cy, radius, thickness, alpha float64, col [3]float64) {
	bounds := img.Bounds()
	outer := radius + thickness
	minX := max(bounds.Min.X, int(cx-outer)-1)
	maxX := min(bounds.Max.X-1, int(cx+outer)+1)
	minY := max(bounds.Min.Y, int(cy-outer)-1)
	maxY := min(bounds.Max.Y-1, int(cy+outer)+1)

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			t := 1 - math.Abs(d-radius)/thickness
			if t <= 0 {
				continue
			}
			a := alpha * smoothstep(t)
			i := img.PixOffset(x, y)
			img.Pix[i] = clamp8(float64(img.Pix[i])*(1-a) + col[0]*255*a)
			img.Pix[i+1] = clamp8(float64(img.Pix[i+1])*(1-a) + col[1]*255*a)
			img.Pix[i+2] = clamp8(float64(img.Pix[i+2])*(1-a) + col[2]*255*a)
		}
	}
}

func hsv(h, s, v float64) [3]float64 {
	h = math.Mod(h, 360)
	if h < 0 {
		h += 360
	}
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g = c, x
	case h < 120:
		r, g = x, c
	case h < 180:
		g, b = c, x
	case h < 240:
		g, b = x, c
	case h < 300:
		r, b = x, c
	default:
		r, b = c, x
	}
	return [3]float64{r + m, g + m, b + m}
}

func clamp01(v float64) float64 {
	return math.Min(1, math.Max(0, v))
}

func smoothstep(t float64) float64 {
	return t * t * (3 - 2*t)
}

func clamp8(v float64) uint8 {
	switch {
	case v <= 0:
		return 0
	case v >= 255:
		return 255
	default:
		return uint8(v + 0.5)
	}
}
