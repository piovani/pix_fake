package dict

import "time"

// SetClock makes the service read the time from now, for the tests of dict_test.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Windows exposes the windows of the statistics at now, as [start, end) pairs.
func Windows(now time.Time) (d90, m12, m60 [2]time.Time) {
	w := windowsAt(now)
	return w.d90, w.m12, w.m60
}
