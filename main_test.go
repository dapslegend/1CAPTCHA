package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"slidecaptcha/captcha"
)

func call(t *testing.T, h http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPFlow(t *testing.T) {
	svc, err := captcha.NewService(captcha.DefaultConfig())
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(svc.Close)

	handler := newHandler(svc, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// The UI must be served.
	rec := call(t, handler, http.MethodGet, "/", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Security check") {
		t.Fatalf("GET / = %d, body %q", rec.Code, rec.Body.String())
	}

	// A challenge must be created with usable image URLs.
	rec = call(t, handler, http.MethodGet, "/api/captcha", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/captcha = %d: %s", rec.Code, rec.Body.String())
	}
	var created createResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	if created.ID == "" || created.BG == "" || created.Piece == "" || created.ExpiresIn <= 0 {
		t.Fatalf("incomplete challenge response: %+v", created)
	}

	for _, image := range []struct{ path, contentType string }{
		{created.BG, "image/jpeg"},
		{created.Piece, "image/png"},
	} {
		rec = call(t, handler, http.MethodGet, image.path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", image.path, rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != image.contentType {
			t.Errorf("GET %s content type = %q, want %q", image.path, got, image.contentType)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("GET %s returned an empty body", image.path)
		}
	}

	// Answer a challenge created directly so the test knows the secret.
	ch, err := svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	track := humanTrack(ch.AnswerX)

	payload, _ := json.Marshal(map[string]any{"id": ch.ID, "x": ch.AnswerX, "track": track})
	rec = call(t, handler, http.MethodPost, "/api/captcha/verify", payload)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify = %d: %s", rec.Code, rec.Body.String())
	}
	var verified struct {
		OK     bool   `json:"ok"`
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &verified); err != nil || !verified.OK || verified.Ticket == "" {
		t.Fatalf("bad verify response %q (err %v)", rec.Body.String(), err)
	}

	// The protected action accepts the ticket once.
	comment, _ := json.Marshal(map[string]any{"ticket": verified.Ticket, "text": "hello"})
	rec = call(t, handler, http.MethodPost, "/api/comment", comment)
	if rec.Code != http.StatusOK {
		t.Fatalf("comment = %d: %s", rec.Code, rec.Body.String())
	}
	rec = call(t, handler, http.MethodPost, "/api/comment", comment)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("replayed comment = %d, want 403", rec.Code)
	}

	// A wrong position is rejected and burns the challenge.
	ch, err = svc.NewChallenge()
	if err != nil {
		t.Fatalf("NewChallenge: %v", err)
	}
	wrong := ch.AnswerX + 50
	payload, _ = json.Marshal(map[string]any{"id": ch.ID, "x": wrong, "track": humanTrack(wrong)})
	rec = call(t, handler, http.MethodPost, "/api/captcha/verify", payload)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "wrong_position") {
		t.Fatalf("wrong position = %d: %s", rec.Code, rec.Body.String())
	}
}

// humanTrack mirrors the shape of a real drag: eased speed, wobble, irregular
// timing, coalesced events and a small overshoot before settling.
func humanTrack(target int) []captcha.Point {
	const steps = 24
	track := make([]captcha.Point, 0, steps+1)
	var t int64
	for i := 0; i <= steps; i++ {
		u := float64(i) / steps
		eased := u * u * (3 - 2*u)
		x := float64(target) * eased
		if steps-2 <= i && i < steps {
			x += 2.5
		}
		track = append(track, captcha.Point{
			X:         x,
			Y:         42 + 2*math.Sin(float64(i)/2),
			T:         t,
			Coalesced: 1 + i%2,
			Kind:      "p",
			Trusted:   true,
		})
		t += 16 + int64((i*7)%23)
	}
	return track
}
