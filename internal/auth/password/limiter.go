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

const (
	forgetAfter = time.Hour
	maxKeys     = 10000
)

func newLimiter(free int, max time.Duration) *limiter {
	return &limiter{free: free, max: max, now: time.Now, m: map[string]*strikes{}}
}

// take counts an attempt for key before it runs, so parallel attempts cannot
// all pass before the first failure lands. It returns how long key must wait
// instead; zero means go ahead. A successful attempt is handed back with
// forgive or reset.
func (l *limiter) take(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	s := l.m[key]
	if s != nil {
		if d := s.until.Sub(now); d > 0 {
			return d
		}
	}
	if s == nil || now.Sub(s.seen) > forgetAfter {
		l.makeRoom(now)
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
	return 0
}

// makeRoom drops stale keys and, if a flood of fresh ones still fills the
// map, all of them: forgetting strikes beats unbounded memory.
func (l *limiter) makeRoom(now time.Time) {
	if len(l.m) < maxKeys {
		return
	}
	for k, s := range l.m {
		if now.Sub(s.seen) > forgetAfter {
			delete(l.m, k)
		}
	}
	if len(l.m) >= maxKeys {
		clear(l.m)
	}
}

// forgive takes back one counted attempt that succeeded.
func (l *limiter) forgive(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s := l.m[key]; s != nil {
		s.fails--
		if s.fails <= l.free {
			s.until = time.Time{}
		}
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, key)
}
