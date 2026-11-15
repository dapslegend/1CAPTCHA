package captcha

import (
	"sync"
	"time"
)

// maxPendingChallenges bounds memory use. Each entry holds two encoded images,
// so the cap is deliberately modest.
const maxPendingChallenges = 2000

type challengeStore struct {
	mu      sync.Mutex
	pending map[string]*Challenge
}

func newChallengeStore() *challengeStore {
	return &challengeStore{pending: make(map[string]*Challenge)}
}

func (s *challengeStore) put(c *Challenge) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.pending) >= maxPendingChallenges {
		for id := range s.pending {
			delete(s.pending, id)
			if len(s.pending) < maxPendingChallenges*9/10 {
				break
			}
		}
	}
	s.pending[c.ID] = c
}

func (s *challengeStore) get(id string) (*Challenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pending[id]
	return c, ok
}

// take removes the challenge so that it can be answered at most once.
func (s *challengeStore) take(id string) (*Challenge, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.pending[id]
	if ok {
		delete(s.pending, id)
	}
	return c, ok
}

func (s *challengeStore) sweep(now time.Time, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, c := range s.pending {
		if now.Sub(c.Created) > ttl {
			delete(s.pending, id)
		}
	}
}
