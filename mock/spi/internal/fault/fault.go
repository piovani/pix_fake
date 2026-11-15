// Package fault lets a test make the mock misbehave on demand: answer 429 or 503, or answer late. It is how a
// client's handling of rate limiting, unavailability and slowness is tried against the mock.
//
// Rules are added and removed through the control API (/mock/faults). They apply to the Bacen API paths and never
// to the control API or the health check.
package fault

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Rule says which requests misbehave and how.
type Rule struct {
	// Method and PathPrefix select the requests; empty selects all of them.
	Method     string `json:"method,omitempty"`
	PathPrefix string `json:"path_prefix,omitempty"`
	// Status is 429 or 503; zero only delays.
	Status int `json:"status,omitempty"`
	// RetryAfter is the Retry-After header, in seconds, sent with the status.
	RetryAfter int `json:"retry_after,omitempty"`
	// DelayMS holds the request for this long before answering (or before passing it on, with no Status).
	DelayMS int `json:"delay_ms,omitempty"`
	// Count is how many matching requests it affects; zero means until it is removed.
	Count int `json:"count,omitempty"`
}

// Validate checks a rule before it is stored.
func (r Rule) Validate() error {
	switch {
	case r.Status != 0 && r.Status != http.StatusTooManyRequests && r.Status != http.StatusServiceUnavailable:
		return errors.New("status must be 429 or 503 (or omitted, to only delay)")
	case r.Status == 0 && r.DelayMS <= 0:
		return errors.New("a rule needs a status or a delay_ms")
	case r.DelayMS < 0 || r.DelayMS > 60000:
		return errors.New("delay_ms must be between 0 and 60000")
	case r.RetryAfter < 0 || r.RetryAfter > 3600:
		return errors.New("retry_after must be between 0 and 3600 seconds")
	case r.Count < 0:
		return errors.New("count cannot be negative")
	case r.Method != "" && r.Method != strings.ToUpper(r.Method):
		return errors.New("method must be upper case, such as GET")
	case r.PathPrefix != "" && !strings.HasPrefix(r.PathPrefix, "/"):
		return errors.New("path_prefix must start with /")
	case strings.HasPrefix(r.PathPrefix, "/mock"):
		return errors.New("rules cannot target the control API")
	}
	return nil
}

// Active is a stored rule with its identifier and how many requests it can still affect.
type Active struct {
	ID        int  `json:"id"`
	Rule      Rule `json:"rule"`
	Remaining int  `json:"remaining,omitempty"` // zero: unlimited
}

// Set holds the rules. The zero value is ready to use.
type Set struct {
	mu     sync.Mutex
	nextID int
	rules  []*Active
}

// Add stores a rule and returns its identifier.
func (s *Set) Add(r Rule) (int, error) {
	if err := r.Validate(); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	s.rules = append(s.rules, &Active{ID: s.nextID, Rule: r, Remaining: r.Count})
	return s.nextID, nil
}

// Remove deletes a rule; it reports whether it existed.
func (s *Set) Remove(id int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.rules {
		if a.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return true
		}
	}
	return false
}

// Clear deletes all rules.
func (s *Set) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = nil
}

// List returns the rules, oldest first.
func (s *Set) List() []Active {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Active, len(s.rules))
	for i, a := range s.rules {
		out[i] = *a
	}
	return out
}

// take returns the first rule that matches the request and counts the request against it.
func (s *Set) take(r *http.Request) (Rule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, a := range s.rules {
		if a.Rule.Method != "" && a.Rule.Method != r.Method {
			continue
		}
		if !strings.HasPrefix(r.URL.Path, a.Rule.PathPrefix) {
			continue
		}
		rule := a.Rule
		if a.Rule.Count > 0 {
			a.Remaining--
			if a.Remaining <= 0 {
				s.rules = append(s.rules[:i], s.rules[i+1:]...)
			}
		}
		return rule, true
	}
	return Rule{}, false
}

// Middleware applies the rules to the requests that are not exempt. respond writes the error response of the mock
// for a status (Retry-After is already set).
func (s *Set) Middleware(next http.Handler, exempt []string, respond func(w http.ResponseWriter, r *http.Request, status int)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, p := range exempt {
			if strings.HasPrefix(r.URL.Path, p) {
				next.ServeHTTP(w, r)
				return
			}
		}
		rule, ok := s.take(r)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if rule.DelayMS > 0 {
			select {
			case <-time.After(time.Duration(rule.DelayMS) * time.Millisecond):
			case <-r.Context().Done():
				return
			}
		}
		if rule.Status == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if rule.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(rule.RetryAfter))
		}
		respond(w, r, rule.Status)
	})
}
