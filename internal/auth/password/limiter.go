package password

import (
	"sync"
	"time"
)

// limiter backs off after repeated failures for one key: free attempts
// first, then a wait that doubles with each failure up to max. State is per
// replica and in memory; a restart forgets it.
type limiter struct {
	free int
	max  time.Duration
	now  func() time.Time

	mu sync.Mutex
	m  map[string]*strikes
}

type strikes struct {
	fails int
	until time.Time // no attempts before this
	seen  time.Time
}

const forgetAfter = time.Hour

func newLimiter(free int, max time.Duration) *limiter {
	return &limiter{free: free, max: max, now: time.Now, m: map[string]*strikes{}}
}

// wait is how long key must wait before its next attempt; zero means go ahead.
func (l *limiter) wait(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := l.m[key]
	if s == nil {
		return 0
	}
	if d := s.until.Sub(l.now()); d > 0 {
		return d
	}
	return 0
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if len(l.m) > 10000 {
		for k, s := range l.m {
			if now.Sub(s.seen) > forgetAfter {
				delete(l.m, k)
			}
		}
	}
	s := l.m[key]
	if s == nil || now.Sub(s.seen) > forgetAfter {
		s = &strikes{}
		l.m[key] = s
	}
	s.fails++
	s.seen = now
	if over := s.fails - l.free; over > 0 {
		d := l.max
		if over < 20 && time.Second<<(over-1) < l.max {
			d = time.Second << (over - 1)
		}
		s.until = now.Add(d)
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}
