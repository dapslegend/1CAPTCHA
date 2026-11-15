package captcha

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	mrand "math/rand"
	"sync"
	"time"
)

var (
	ErrUnknownChallenge = errors.New("captcha: unknown or already used challenge")
	ErrExpiredChallenge = errors.New("captcha: challenge expired")
	ErrWrongPosition    = errors.New("captcha: piece is not in the right position")
	ErrSuspiciousTrack  = errors.New("captcha: interaction data looks automated")
	ErrInvalidTicket    = errors.New("captcha: invalid ticket")
	ErrExpiredTicket    = errors.New("captcha: ticket expired")
	ErrTicketUsed       = errors.New("captcha: ticket already used")
)

// Point is one pointer sample collected while the piece is dragged. X is the
// piece's left edge and Y the pointer position, both in image pixels. T is the
// milliseconds elapsed since the drag started. Coalesced is the number of
// hardware events the browser merged into this sample, Kind is "p" for pointer
// input or "k" for keyboard input, and Trusted mirrors the browser's
// isTrusted flag — false for events a script dispatched itself.
type Point struct {
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	T         int64   `json:"t"`
	Coalesced int     `json:"c,omitempty"`
	Kind      string  `json:"k,omitempty"`
	Trusted   bool    `json:"tr,omitempty"`
}

// Env carries client environment signals submitted alongside the track. They
// are client-reported and therefore spoofable, but they catch the large class
// of automation that does not bother to lie.
type Env struct {
	WebDriver   bool   `json:"wd"`
	PointerType string `json:"pt,omitempty"`
}

// Challenge is a generated captcha. AnswerX is the secret horizontal position
// the piece must be dropped at; it must never be sent to the client.
type Challenge struct {
	ID      string
	AnswerX int
	PieceY  int
	// DecoyX/DecoyY mark a decoy socket. Like the socket itself it is visible
	// in the image, so it is not secret, but it is never sent to the client.
	DecoyX  int
	DecoyY  int
	PieceW  int
	PieceH  int
	Width   int
	Height  int
	BG      []byte // JPEG
	Piece   []byte // PNG with alpha
	Created time.Time
}

// Service generates challenges, verifies answers and issues tickets.
type Service struct {
	cfg     Config
	store   *challengeStore
	tickets *ticketStore
	now     func() time.Time
	stop    chan struct{}
	once    sync.Once
}

func NewService(cfg Config) (*Service, error) {
	cfg = cfg.withDefaults()
	if len(cfg.Secret) == 0 {
		secret, err := randomBytes(32)
		if err != nil {
			return nil, err
		}
		cfg.Secret = secret
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	s := &Service{
		cfg:     cfg,
		store:   newChallengeStore(),
		tickets: newTicketStore(cfg.Secret, cfg.TicketTTL),
		now:     time.Now,
		stop:    make(chan struct{}),
	}
	go s.sweepLoop()
	return s, nil
}

// Close stops the background sweeper. It does not invalidate issued tickets.
func (s *Service) Close() {
	s.once.Do(func() { close(s.stop) })
}

func (s *Service) ChallengeTTL() time.Duration { return s.cfg.ChallengeTTL }
func (s *Service) TicketTTL() time.Duration    { return s.cfg.TicketTTL }

func (s *Service) NewChallenge() (*Challenge, error) {
	seed, err := randomSeed()
	if err != nil {
		return nil, err
	}
	idBytes, err := randomBytes(16)
	if err != nil {
		return nil, err
	}

	art, err := render(s.cfg, mrand.New(mrand.NewSource(seed)))
	if err != nil {
		return nil, err
	}

	ch := &Challenge{
		ID:      hex.EncodeToString(idBytes),
		AnswerX: art.answerX,
		PieceY:  art.answerY,
		DecoyX:  art.decoyX,
		DecoyY:  art.decoyY,
		PieceW:  s.cfg.PieceWidth + 2*s.cfg.Knob,
		PieceH:  s.cfg.PieceHeight + 2*s.cfg.Knob,
		Width:   s.cfg.Width,
		Height:  s.cfg.Height,
		BG:      art.background,
		Piece:   art.piece,
		Created: s.now(),
	}
	s.store.put(ch)
	return ch, nil
}

// Challenge returns a pending challenge so its images can be served.
func (s *Service) Challenge(id string) (*Challenge, bool) {
	return s.store.get(id)
}

// Verify consumes the challenge and, on success, returns a single-use ticket.
// Any verification attempt burns the challenge, which makes guessing the
// position economically pointless.
func (s *Service) Verify(id string, x int, track []Point, env Env) (string, error) {
	ch, ok := s.store.take(id)
	if !ok {
		return "", ErrUnknownChallenge
	}
	if s.now().Sub(ch.Created) > s.cfg.ChallengeTTL {
		return "", ErrExpiredChallenge
	}
	if err := analyzeTrack(track, x, env); err != nil {
		return "", err
	}
	if abs(x-ch.AnswerX) > s.cfg.Tolerance {
		return "", ErrWrongPosition
	}
	return s.tickets.issue(s.now())
}

// RedeemTicket validates a ticket and marks it used.
func (s *Service) RedeemTicket(ticket string) error {
	if ticket == "" {
		return ErrInvalidTicket
	}
	return s.tickets.redeem(ticket, s.now())
}

func (s *Service) sweepLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-ticker.C:
			s.store.sweep(now, s.cfg.ChallengeTTL)
			s.tickets.sweep(now)
		}
	}
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func randomSeed() (int64, error) {
	b, err := randomBytes(8)
	if err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(b)), nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
