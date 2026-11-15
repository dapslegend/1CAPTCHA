package captcha

import (
	"bytes"
	"errors"
	"image/jpeg"
	"image/png"
	"math"
	mrand "math/rand"
	"testing"
	"time"
)

func testService(t *testing.T, mutate func(*Config)) *Service {
	t.Helper()
	cfg := DefaultConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc
}

// humanTrack simulates a realistic drag: eased speed, hand wobble, irregular
// event timing, coalesced hardware events and a small overshoot correction.
func humanTrack(target int) []Point {
	const steps = 24
	track := make([]Point, 0, steps+1)
	var t int64
	for i := 0; i <= steps; i++ {
		u := float64(i) / steps
		eased := u * u * (3 - 2*u)
		x := float64(target) * eased
		// A hand overshoots the target near the end and settles back.
		if steps-2 <= i && i < steps {
			x += 2.5
		}
		track = append(track, Point{
			X:         x,
			Y:         60 + 2*math.Sin(float64(i)/2),
			T:         t,
			Coalesced: 1 + i%2,
			Kind:      "p",
			Trusted:   true,
		})
		t += 16 + int64((i*7)%23)
	}
	return track
}

// botTrack is a naive linear automation: constant speed, straight line, fixed
// event interval, no coalescing, script-dispatched (untrusted) events.
func botTrack(target int) []Point {
	const steps = 20
	track := make([]Point, 0, steps+1)
	for i := 0; i <= steps; i++ {
		track = append(track, Point{
			X:         float64(target) * float64(i) / steps,
			Y:         50,
			T:         int64(i) * 30,
			Coalesced: 1,
			Kind:      "p",
		})
	}
	return track
}

// cdpTrack mimics browser-level automation (Selenium/Playwright): the events
// are genuinely trusted, but the motion is still perfectly linear and evenly
// timed, and no course correction ever happens.
func cdpTrack(target int) []Point {
	const steps = 22
	track := make([]Point, 0, steps+1)
	for i := 0; i <= steps; i++ {
		track = append(track, Point{
			X:         float64(target) * float64(i) / steps,
			Y:         50,
			T:         int64(i) * 33,
			Coalesced: 1,
			Kind:      "p",
			Trusted:   true,
		})
	}
	return track
}

func keyboardTrack(target int) []Point {
	var track []Point
	var t int64
	x := 0
	for target-x > 4 {
		track = append(track, Point{X: float64(x), Y: 40, T: t, Kind: "k"})
		t += 40
		x += 4
	}
	track = append(track, Point{X: float64(x), Y: 40, T: t, Kind: "k"})
	t += 40
	track = append(track, Point{X: float64(target), Y: 40, T: t, Kind: "k"})
	return track
}

func TestPuzzleMaskShape(t *testing.T) {
	mask := puzzleMask(48, 48, 10)
	if got := mask.Bounds().Dx(); got != 68 {
		t.Fatalf("mask width = %d, want 68", got)
	}
	if got := mask.Bounds().Dy(); got != 68 {
		t.Fatalf("mask height = %d, want 68", got)
	}

	at := func(x, y int) uint8 { return mask.Pix[mask.PixOffset(x, y)] }

	if got := at(10+24, 10+24); got != 255 {
		t.Errorf("body centre alpha = %d, want 255", got)
	}
	if got := at(34, 3); got != 255 {
		t.Errorf("knob alpha = %d, want 255 (knob must protrude above the body)", got)
	}
	if got := at(0, 0); got != 0 {
		t.Errorf("corner alpha = %d, want 0", got)
	}
	if got := at(56, 34); got != 0 {
		t.Errorf("notch alpha = %d, want 0 (notch must be cut out)", got)
	}
}

func TestRenderProducesUsableArtwork(t *testing.T) {
	cfg := DefaultConfig()
	art, err := render(cfg, mrand.New(mrand.NewSource(7)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	bg, err := jpeg.Decode(bytes.NewReader(art.background))
	if err != nil {
		t.Fatalf("decode background: %v", err)
	}
	if bg.Bounds().Dx() != cfg.Width || bg.Bounds().Dy() != cfg.Height {
		t.Fatalf("background is %v, want %dx%d", bg.Bounds(), cfg.Width, cfg.Height)
	}

	piece, err := png.Decode(bytes.NewReader(art.piece))
	if err != nil {
		t.Fatalf("decode piece: %v", err)
	}
	boxW, boxH := cfg.PieceWidth+2*cfg.Knob, cfg.PieceHeight+2*cfg.Knob
	if piece.Bounds().Dx() != boxW || piece.Bounds().Dy() != boxH {
		t.Fatalf("piece is %v, want %dx%d", piece.Bounds(), boxW, boxH)
	}

	if art.answerX < boxW+20 || art.answerX > cfg.Width-boxW-4 {
		t.Fatalf("answerX = %d outside the reachable range", art.answerX)
	}
	if art.answerY < 4 || art.answerY > cfg.Height-boxH-8+4 {
		t.Fatalf("answerY = %d outside the reachable range", art.answerY)
	}

	// The piece must actually contain opaque pixels and transparent padding.
	var opaque, transparent int
	for y := 0; y < boxH; y++ {
		for x := 0; x < boxW; x++ {
			_, _, _, a := piece.At(x, y).RGBA()
			switch {
			case a == 0xffff:
				opaque++
			case a == 0:
				transparent++
			}
		}
	}
	if opaque == 0 || transparent == 0 {
		t.Fatalf("piece alpha looks wrong: opaque=%d transparent=%d", opaque, transparent)
	}
}

func TestPieceExactlyCoversTheSocket(t *testing.T) {
	cfg := DefaultConfig()
	art, err := render(cfg, mrand.New(mrand.NewSource(11)))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	bg, err := jpeg.Decode(bytes.NewReader(art.background))
	if err != nil {
		t.Fatalf("decode background: %v", err)
	}
	piece, err := png.Decode(bytes.NewReader(art.piece))
	if err != nil {
		t.Fatalf("decode piece: %v", err)
	}

	luma := func(r, g, b uint32) float64 {
		return 0.2126*float64(r>>8) + 0.7152*float64(g>>8) + 0.0722*float64(b>>8)
	}

	// Sample the inside of the piece, skipping the knob/notch edges.
	inset := cfg.Knob + 6
	var socket, covered float64
	var samples int
	for y := inset; y < cfg.PieceHeight+cfg.Knob; y++ {
		for x := inset; x < cfg.PieceWidth+cfg.Knob; x++ {
			br, bgc, bb, _ := bg.At(art.answerX+x, art.answerY+y).RGBA()
			pr, pg, pb, pa := piece.At(x, y).RGBA()

			socket += luma(br, bgc, bb)
			// Straight alpha compositing of the piece over the background.
			a := float64(pa) / 65535
			covered += luma(
				uint32(float64(pr)+float64(br)*(1-a)),
				uint32(float64(pg)+float64(bgc)*(1-a)),
				uint32(float64(pb)+float64(bb)*(1-a)),
			)
			samples++
		}
	}

	socketAvg := socket / float64(samples)
	coveredAvg := covered / float64(samples)
	if coveredAvg < socketAvg*1.8 {
		t.Fatalf("piece does not restore the socket: socket luma %.1f, composited luma %.1f", socketAvg, coveredAvg)
	}
}

func TestVerifyAcceptsCorrectPositionOnce(t *testing.T) {
	svc := testService(t, nil)

	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}

	ticket, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), Env{})
	if err != nil {
		t.Fatalf("Verify at answer: %v", err)
	}
	if ticket == "" {
		t.Fatal("Verify returned an empty ticket")
	}
	if err := svc.RedeemTicket(ticket); err != nil {
		t.Fatalf("RedeemTicket: %v", err)
	}

	// Challenges are single-use.
	if _, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), Env{}); !errors.Is(err, ErrUnknownChallenge) {
		t.Fatalf("second Verify error = %v, want %v", err, ErrUnknownChallenge)
	}
}

func TestVerifyRejectsWrongPosition(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}

	x := ch.AnswerX + 40
	if _, err := svc.Verify(ch.ID, x, humanTrack(x), Env{}); !errors.Is(err, ErrWrongPosition) {
		t.Fatalf("Verify error = %v, want %v", err, ErrWrongPosition)
	}
}

func TestVerifyRejectsAutomatedTracks(t *testing.T) {
	svc := testService(t, nil)

	cases := map[string][]Point{
		"too few samples": {{X: 0, Y: 0, T: 0}, {X: 150, Y: 0, T: 400}},
		"too fast": {
			{X: 0, Y: 0, T: 0}, {X: 40, Y: 0, T: 10}, {X: 80, Y: 0, T: 20},
			{X: 120, Y: 0, T: 30}, {X: 160, Y: 0, T: 40}, {X: 200, Y: 0, T: 50},
		},
		"no movement": {
			{X: 90, Y: 0, T: 0}, {X: 90, Y: 0, T: 100}, {X: 90, Y: 0, T: 200},
			{X: 90, Y: 0, T: 300}, {X: 90, Y: 0, T: 400}, {X: 90, Y: 0, T: 500},
		},
	}

	for name, track := range cases {
		t.Run(name, func(t *testing.T) {
			ch, err := svc.NewChallenge()
			if err != nil {
				t.Fatalf("NewChallenge: %v", err)
			}
			if _, err := svc.Verify(ch.ID, ch.AnswerX, track, Env{}); !errors.Is(err, ErrSuspiciousTrack) {
				t.Fatalf("Verify error = %v, want %v", err, ErrSuspiciousTrack)
			}
		})
	}
}

func TestVerifyRejectsMismatchedTrack(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	// Track ends far away from the reported position.
	if _, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX-100), Env{}); !errors.Is(err, ErrSuspiciousTrack) {
		t.Fatalf("Verify error = %v, want %v", err, ErrSuspiciousTrack)
	}
}

func TestVerifyRejectsLinearBot(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	if _, err := svc.Verify(ch.ID, ch.AnswerX, botTrack(ch.AnswerX), Env{}); !errors.Is(err, ErrSuspiciousTrack) {
		t.Fatalf("Verify error = %v, want %v", err, ErrSuspiciousTrack)
	}
}

func TestVerifyRejectsBrowserAutomation(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	// Trusted events, but robotic motion and timing: still rejected.
	if _, err := svc.Verify(ch.ID, ch.AnswerX, cdpTrack(ch.AnswerX), Env{}); !errors.Is(err, ErrSuspiciousTrack) {
		t.Fatalf("Verify error = %v, want %v", err, ErrSuspiciousTrack)
	}
}

func TestVerifyRejectsWebDriverEnv(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	env := Env{WebDriver: true, PointerType: "mouse"}
	if _, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), env); !errors.Is(err, ErrSuspiciousTrack) {
		t.Fatalf("Verify error = %v, want %v", err, ErrSuspiciousTrack)
	}
}

func TestVerifyAcceptsKeyboardTrack(t *testing.T) {
	svc := testService(t, nil)
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	if _, err := svc.Verify(ch.ID, ch.AnswerX, keyboardTrack(ch.AnswerX), Env{}); err != nil {
		t.Fatalf("keyboard Verify: %v", err)
	}
}

func TestDecoySocketIsRejected(t *testing.T) {
	svc := testService(t, nil)

	var ch *Challenge
	for i := 0; i < 25 && ch == nil; i++ {
		c, err := svc.NewChallenge()
		if err != nil {
			t.Fatalf("NewChallenge: %v", err)
		}
		if c.DecoyX != 0 || c.DecoyY != 0 {
			ch = c
		}
	}
	if ch == nil {
		t.Skip("no decoy was placed in 25 challenges")
	}

	box := svc.cfg.PieceWidth + 2*svc.cfg.Knob
	if abs(ch.AnswerX-ch.DecoyX) < box+12 && abs(ch.PieceY-ch.DecoyY) < box+12 {
		t.Fatalf("decoy at (%d,%d) overlaps the answer at (%d,%d)", ch.DecoyX, ch.DecoyY, ch.AnswerX, ch.PieceY)
	}

	if _, err := svc.Verify(ch.ID, ch.DecoyX, humanTrack(ch.DecoyX), Env{}); !errors.Is(err, ErrWrongPosition) {
		t.Fatalf("Verify at decoy error = %v, want %v", err, ErrWrongPosition)
	}
}

func TestExpiredChallenge(t *testing.T) {
	svc := testService(t, nil)
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	clock = clock.Add(svc.ChallengeTTL() + time.Second)

	if _, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), Env{}); !errors.Is(err, ErrExpiredChallenge) {
		t.Fatalf("Verify error = %v, want %v", err, ErrExpiredChallenge)
	}
}

func TestTicketRules(t *testing.T) {
	svc := testService(t, func(c *Config) { c.TicketTTL = time.Minute })
	clock := time.Now()
	svc.now = func() time.Time { return clock }

	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	ticket, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), Env{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if err := svc.RedeemTicket(ticket); err != nil {
		t.Fatalf("first redemption: %v", err)
	}
	if err := svc.RedeemTicket(ticket); !errors.Is(err, ErrTicketUsed) {
		t.Fatalf("replay error = %v, want %v", err, ErrTicketUsed)
	}

	tampered := ticket[:len(ticket)-1] + "A"
	if err := svc.RedeemTicket(tampered); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("tampered ticket error = %v, want %v", err, ErrInvalidTicket)
	}

	second := mustIssue(t, svc)
	clock = clock.Add(2 * time.Minute)
	if err := svc.RedeemTicket(second); !errors.Is(err, ErrExpiredTicket) {
		t.Fatalf("expired ticket error = %v, want %v", err, ErrExpiredTicket)
	}
}

func mustIssue(t *testing.T, svc *Service) string {
	t.Helper()
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	ticket, err := svc.Verify(ch.ID, ch.AnswerX, humanTrack(ch.AnswerX), Env{})
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return ticket
}
