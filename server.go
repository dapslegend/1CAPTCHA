package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"slidecaptcha/captcha"
)

type api struct {
	svc        *captcha.Service
	log        *slog.Logger
	createLim  *limiter
	verifyLim  *limiter
	commentLim *limiter
}

func newHandler(svc *captcha.Service, log *slog.Logger) http.Handler {
	a := &api{
		svc:        svc,
		log:        log,
		createLim:  newLimiter(60, time.Minute),
		verifyLim:  newLimiter(40, time.Minute),
		commentLim: newLimiter(20, time.Minute),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/captcha", a.handleCreate)
	mux.HandleFunc("GET /api/captcha/{id}/bg.jpg", a.handleImage("bg"))
	mux.HandleFunc("GET /api/captcha/{id}/piece.png", a.handleImage("piece"))
	mux.HandleFunc("POST /api/captcha/verify", a.handleVerify)
	mux.HandleFunc("POST /api/comment", a.handleComment)

	static, err := fs.Sub(webFS, "web")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(static)))

	return withSecurityHeaders(withLogging(log, mux))
}

type createResponse struct {
	ID          string `json:"id"`
	BG          string `json:"bg"`
	Piece       string `json:"piece"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	PieceWidth  int    `json:"pieceWidth"`
	PieceHeight int    `json:"pieceHeight"`
	PieceY      int    `json:"pieceY"`
	ExpiresIn   int    `json:"expiresIn"`
}

func (a *api) handleCreate(w http.ResponseWriter, r *http.Request) {
	if !a.createLim.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		a.fail(w, http.StatusTooManyRequests, "rate_limited", "Too many challenges requested. Try again shortly.")
		return
	}

	ch, err := a.svc.NewChallenge()
	if err != nil {
		a.log.Error("create challenge", "err", err)
		a.fail(w, http.StatusInternalServerError, "internal", "Could not create a challenge.")
		return
	}

	base := "/api/captcha/" + ch.ID
	writeJSON(w, http.StatusOK, createResponse{
		ID:          ch.ID,
		BG:          base + "/bg.jpg",
		Piece:       base + "/piece.png",
		Width:       ch.Width,
		Height:      ch.Height,
		PieceWidth:  ch.PieceW,
		PieceHeight: ch.PieceH,
		PieceY:      ch.PieceY,
		ExpiresIn:   int(a.svc.ChallengeTTL().Seconds()),
	})
}

func (a *api) handleImage(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ch, ok := a.svc.Challenge(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}

		var body []byte
		var contentType string
		switch kind {
		case "bg":
			body, contentType = ch.BG, "image/jpeg"
		default:
			body, contentType = ch.Piece, "image/png"
		}

		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}
}

type verifyRequest struct {
	ID    string          `json:"id"`
	X     int             `json:"x"`
	Track []captcha.Point `json:"track"`
	Env   captcha.Env     `json:"env"`
}

func (a *api) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !a.verifyLim.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		a.fail(w, http.StatusTooManyRequests, "rate_limited", "Too many attempts. Try again shortly.")
		return
	}

	var req verifyRequest
	if err := decodeJSON(w, r, &req); err != nil || req.ID == "" {
		a.fail(w, http.StatusBadRequest, "bad_request", "Malformed verification request.")
		return
	}

	ticket, err := a.svc.Verify(req.ID, req.X, req.Track, req.Env)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":        true,
			"ticket":    ticket,
			"expiresIn": int(a.svc.TicketTTL().Seconds()),
		})
	case errors.Is(err, captcha.ErrWrongPosition):
		a.fail(w, http.StatusBadRequest, "wrong_position", "The piece is not in the right place. Try again.")
	case errors.Is(err, captcha.ErrSuspiciousTrack):
		a.fail(w, http.StatusBadRequest, "behavior", "That interaction did not look human. Try again.")
	case errors.Is(err, captcha.ErrExpiredChallenge), errors.Is(err, captcha.ErrUnknownChallenge):
		a.fail(w, http.StatusBadRequest, "challenge_expired", "This challenge is no longer valid.")
	default:
		a.log.Error("verify", "err", err)
		a.fail(w, http.StatusInternalServerError, "internal", "Could not verify the challenge.")
	}
}

type commentRequest struct {
	Ticket string `json:"ticket"`
	Text   string `json:"text"`
}

// handleComment stands in for a protected action: it only accepts a request
// that carries a valid, unused, unexpired captcha ticket.
func (a *api) handleComment(w http.ResponseWriter, r *http.Request) {
	if !a.commentLim.allow(clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		a.fail(w, http.StatusTooManyRequests, "rate_limited", "Slow down.")
		return
	}

	var req commentRequest
	if err := decodeJSON(w, r, &req); err != nil {
		a.fail(w, http.StatusBadRequest, "bad_request", "Malformed request.")
		return
	}
	if err := a.svc.RedeemTicket(req.Ticket); err != nil {
		code := "invalid_ticket"
		switch {
		case errors.Is(err, captcha.ErrExpiredTicket):
			code = "expired_ticket"
		case errors.Is(err, captcha.ErrTicketUsed):
			code = "used_ticket"
		}
		a.fail(w, http.StatusForbidden, code, "A valid captcha ticket is required.")
		return
	}
	if len(req.Text) == 0 || len(req.Text) > 300 {
		a.fail(w, http.StatusBadRequest, "bad_request", "Comment must be 1-300 characters.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"comment": req.Text,
		"message": "Ticket accepted. In a real application this is where the action runs.",
	})
}

func (a *api) fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"ok":      false,
		"code":    code,
		"message": message,
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	return json.NewDecoder(r.Body).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; "+
				"connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func withLogging(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration", time.Since(start).Round(time.Microsecond),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
		r.ResponseWriter.WriteHeader(code)
	}
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += n
	return n, err
}
