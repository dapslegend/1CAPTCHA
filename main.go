// Command slidecaptcha serves a drag-to-fit image captcha.
//
// Flow:
//
//  1. GET /api/captcha paints a background, cuts a jigsaw-shaped piece out of
//     it, shades the socket, and stores the answer position server-side. The
//     response carries only image URLs; the answer never leaves the server.
//  2. The browser lets the user drag the piece and records pointer samples.
//  3. POST /api/captcha/verify consumes the challenge, checks the drop position
//     against the tolerance and the pointer track against liveness heuristics,
//     then issues a short-lived HMAC-signed ticket.
//  4. POST /api/comment is a stand-in for any protected action: it requires a
//     valid, unused, unexpired ticket.
//
// Configure with ADDR (listen address, default :8080) and CAPTCHA_SECRET
// (at least 16 bytes; a random one is generated when omitted).
package main

import (
	"context"
	"embed"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"slidecaptcha/captcha"
)

//go:embed all:web
var webFS embed.FS

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg := captcha.DefaultConfig()
	if secret := os.Getenv("CAPTCHA_SECRET"); secret != "" {
		cfg.Secret = []byte(secret)
	}

	svc, err := captcha.NewService(cfg)
	if err != nil {
		logger.Error("configure captcha", "err", err)
		os.Exit(1)
	}
	defer svc.Close()

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           newHandler(svc, logger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	shutdown := make(chan struct{})
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
		<-signals
		logger.Info("shutting down")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error("shutdown", "err", err)
		}
		close(shutdown)
	}()

	logger.Info("captcha demo listening", "url", "http://localhost"+addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("serve", "err", err)
		os.Exit(1)
	}
	<-shutdown
}
