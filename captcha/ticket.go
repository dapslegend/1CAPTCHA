package captcha

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ticketStore issues and redeems short-lived, single-use verification tickets.
//
// A ticket is "<version>.<expiryUnix>.<jti>.<hmac>" where the HMAC covers the
// first three fields. The jti is remembered after the first redemption so a
// replayed ticket is rejected even though verification is stateless otherwise.
type ticketStore struct {
	secret []byte
	ttl    time.Duration

	mu   sync.Mutex
	used map[string]time.Time // jti -> ticket expiry
}

func newTicketStore(secret []byte, ttl time.Duration) *ticketStore {
	return &ticketStore{
		secret: secret,
		ttl:    ttl,
		used:   make(map[string]time.Time),
	}
}

func (s *ticketStore) issue(now time.Time) (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	expiry := now.Add(s.ttl).Unix()
	payload := "v1." + strconv.FormatInt(expiry, 10) + "." + hex.EncodeToString(raw)
	return payload + "." + s.sign(payload), nil
}

func (s *ticketStore) redeem(ticket string, now time.Time) error {
	parts := strings.Split(ticket, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return ErrInvalidTicket
	}
	payload := strings.Join(parts[:3], ".")

	if _, err := base64.RawURLEncoding.DecodeString(parts[3]); err != nil {
		return ErrInvalidTicket
	}
	if !hmac.Equal([]byte(parts[3]), []byte(s.sign(payload))) {
		return ErrInvalidTicket
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return ErrInvalidTicket
	}
	if now.Unix() > expiry {
		return ErrExpiredTicket
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.used[parts[2]]; seen {
		return ErrTicketUsed
	}
	s.used[parts[2]] = time.Unix(expiry, 0)
	return nil
}

func (s *ticketStore) sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for jti, expiry := range s.used {
		if now.After(expiry) {
			delete(s.used, jti)
		}
	}
}

func (s *ticketStore) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
