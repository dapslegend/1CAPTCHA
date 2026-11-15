package captcha

import (
	"math"
	"sort"
)

// minPointerScore is how many human-likeness signals a pointer drag must show
// out of the six scored. A naive linear bot scores 0-1; commodity automation
// driving real input events scores 1-2; a real hand scores 4-6.
const minPointerScore = 3

// analyzeTrack validates the interaction samples. Hard structural failures and
// a too-low human-likeness score both return ErrSuspiciousTrack.
func analyzeTrack(track []Point, x int, env Env) error {
	if env.WebDriver {
		return ErrSuspiciousTrack // headless driver flag is up
	}
	if len(track) < 6 || len(track) > 2000 {
		return ErrSuspiciousTrack
	}
	first, last := track[0], track[len(track)-1]
	elapsed := last.T - first.T
	if elapsed < 280 || elapsed > 10*60*1000 {
		return ErrSuspiciousTrack
	}

	minX, maxX := first.X, first.X
	for _, p := range track {
		if math.IsNaN(p.X) || math.IsNaN(p.Y) || math.IsInf(p.X, 0) || math.IsInf(p.Y, 0) {
			return ErrSuspiciousTrack
		}
		minX = math.Min(minX, p.X)
		maxX = math.Max(maxX, p.X)
	}
	if maxX-minX < 20 {
		return ErrSuspiciousTrack // the piece never really moved
	}
	if math.Abs(last.X-float64(x)) > 12 {
		return ErrSuspiciousTrack // reported position does not match the drag
	}

	if isKeyboard(track) {
		return checkKeyboard(track)
	}
	if pointerScore(track) < minPointerScore {
		return ErrSuspiciousTrack
	}
	return nil
}

func isKeyboard(track []Point) bool {
	for _, p := range track {
		if p.Kind != "k" {
			return false
		}
	}
	return true
}

// checkKeyboard validates the discrete structure of keyboard input: steps of
// exactly one step size and a constant vertical position.
func checkKeyboard(track []Point) error {
	for i := 1; i < len(track); i++ {
		dx := math.Abs(track[i].X - track[i-1].X)
		if i < len(track)-1 && dx != 1 && dx != 4 {
			return ErrSuspiciousTrack
		}
		if dx > 4.01 {
			return ErrSuspiciousTrack
		}
		if math.Abs(track[i].Y-track[i-1].Y) > 0.01 {
			return ErrSuspiciousTrack
		}
	}
	return nil
}

// pointerScore awards one point per independent human-likeness signal.
func pointerScore(track []Point) int {
	score := 0
	if wobble(track) >= 1.2 {
		score++
	}
	if velocityVariation(track) >= 0.18 {
		score++
	}
	if hasCoalesced(track) {
		score++
	}
	if timingVariety(track) >= 3 {
		score++
	}
	if hasTrustedEvents(track) {
		score++
	}
	if hasCourseCorrection(track) {
		score++
	}
	return score
}

// wobble is the largest vertical deviation from the median y. Real hands do not
// drag in a perfectly straight line.
func wobble(track []Point) float64 {
	ys := make([]float64, len(track))
	for i, p := range track {
		ys[i] = p.Y
	}
	sort.Float64s(ys)
	median := ys[len(ys)/2]
	dev := 0.0
	for _, y := range ys {
		dev = math.Max(dev, math.Abs(y-median))
	}
	return dev
}

// velocityVariation is the coefficient of variation of horizontal speed.
// Scripted drags move at a constant rate; humans accelerate, overshoot, settle.
func velocityVariation(track []Point) float64 {
	var speeds []float64
	for i := 1; i < len(track); i++ {
		dt := float64(track[i].T - track[i-1].T)
		if dt <= 0 {
			continue
		}
		speeds = append(speeds, math.Abs(track[i].X-track[i-1].X)/dt)
	}
	if len(speeds) < 4 {
		return 0
	}
	mean := 0.0
	for _, s := range speeds {
		mean += s
	}
	mean /= float64(len(speeds))
	if mean == 0 {
		return 0
	}
	var sq float64
	for _, s := range speeds {
		sq += (s - mean) * (s - mean)
	}
	return math.Sqrt(sq/float64(len(speeds))) / mean
}

// hasCoalesced reports whether any sample came from real high-rate input:
// browsers coalesce hardware pointer events, synthetic dispatch does not.
func hasCoalesced(track []Point) bool {
	for _, p := range track {
		if p.Coalesced >= 2 {
			return true
		}
	}
	return false
}

// timingVariety counts distinct inter-event gaps. Fixed-step automation emits
// one or two gap lengths; real input scheduling produces many.
func timingVariety(track []Point) int {
	seen := make(map[int64]bool, len(track))
	for i := 1; i < len(track); i++ {
		seen[track[i].T-track[i-1].T] = true
	}
	return len(seen)
}

// hasTrustedEvents reports whether at least half of the pointer samples were
// produced by the browser's input pipeline rather than dispatched by a script.
func hasTrustedEvents(track []Point) bool {
	n, trusted := 0, 0
	for _, p := range track {
		if p.Kind == "k" {
			continue
		}
		n++
		if p.Trusted {
			trusted++
		}
	}
	return n > 0 && trusted*2 >= n
}

// hasCourseCorrection reports whether the horizontal path reverses direction
// at least once. Humans overshoot and settle; scripted drags move one way.
func hasCourseCorrection(track []Point) bool {
	changes, lastDir := 0, 0
	for i := 1; i < len(track); i++ {
		dx := track[i].X - track[i-1].X
		if math.Abs(dx) < 0.5 {
			continue // ignore sub-pixel jitter when judging direction
		}
		dir := 1
		if dx < 0 {
			dir = -1
		}
		if lastDir != 0 && dir != lastDir {
			changes++
		}
		lastDir = dir
	}
	return changes >= 1
}
