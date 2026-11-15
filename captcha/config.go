package captcha

import (
	"errors"
	"time"
)

// Config controls how challenges look and how strictly they are verified.
type Config struct {
	// Width and Height are the background image dimensions in pixels.
	Width, Height int

	// PieceWidth and PieceHeight describe the square body of the puzzle piece.
	// The rendered piece is larger because it is padded by Knob on every side
	// to fit the knob and the notch.
	PieceWidth, PieceHeight int

	// Knob is the radius of the knob on the top edge and of the notch on the
	// right edge. Knob also acts as the transparent padding around the piece.
	Knob int

	// Tolerance is the maximum allowed horizontal error, in pixels.
	Tolerance int

	// Decoys is the number of extra sockets painted into the background. The
	// piece only fits one of them, so the solver must judge where it belongs
	// instead of just dragging to the only hole. 0 disables decoys.
	Decoys int

	// ChallengeTTL is how long a challenge can be answered.
	ChallengeTTL time.Duration

	// TicketTTL is how long a verification ticket can be redeemed.
	TicketTTL time.Duration

	// Secret signs verification tickets. If empty, a random secret is
	// generated at startup (tickets then do not survive a restart).
	Secret []byte
}

func DefaultConfig() Config {
	return Config{
		Width:        360,
		Height:       200,
		PieceWidth:   48,
		PieceHeight:  48,
		Knob:         10,
		Tolerance:    6,
		Decoys:       1,
		ChallengeTTL: 2 * time.Minute,
		TicketTTL:    5 * time.Minute,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.Width == 0 {
		c.Width = d.Width
	}
	if c.Height == 0 {
		c.Height = d.Height
	}
	if c.PieceWidth == 0 {
		c.PieceWidth = d.PieceWidth
	}
	if c.PieceHeight == 0 {
		c.PieceHeight = d.PieceHeight
	}
	if c.Knob == 0 {
		c.Knob = d.Knob
	}
	if c.Tolerance == 0 {
		c.Tolerance = d.Tolerance
	}
	if c.Decoys < 0 {
		c.Decoys = d.Decoys
	}
	if c.ChallengeTTL <= 0 {
		c.ChallengeTTL = d.ChallengeTTL
	}
	if c.TicketTTL <= 0 {
		c.TicketTTL = d.TicketTTL
	}
	return c
}

func (c Config) validate() error {
	switch {
	case c.Width < 160 || c.Height < 100:
		return errors.New("captcha: background must be at least 160x100")
	case c.PieceWidth < 24 || c.PieceHeight < 24:
		return errors.New("captcha: piece body must be at least 24x24")
	case c.Knob < 4 || c.Knob > min(c.PieceWidth, c.PieceHeight)/2:
		return errors.New("captcha: knob must be between 4 and half the piece size")
	case c.Width < c.PieceWidth+2*c.Knob+48:
		return errors.New("captcha: background is too narrow for the piece and its travel range")
	case c.Height < c.PieceHeight+2*c.Knob+16:
		return errors.New("captcha: background is too short for the piece")
	case c.Tolerance < 1:
		return errors.New("captcha: tolerance must be positive")
	case c.Decoys > 2:
		return errors.New("captcha: at most 2 decoy sockets are supported")
	case len(c.Secret) < 16:
		return errors.New("captcha: secret must be at least 16 bytes")
	}
	return nil
}
