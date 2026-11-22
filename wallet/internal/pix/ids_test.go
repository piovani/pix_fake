package pix

import (
	"regexp"
	"testing"
	"time"
)

func TestNewEndToEndID(t *testing.T) {
	now := time.Date(2026, 11, 19, 15, 30, 59, 0, time.FixedZone("BRT", -3*3600)) // 18:30 UTC
	id, err := NewEndToEndID("99000001", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 || id[:21] != "E99000001202611191830" {
		t.Fatalf("id = %q: the date must be in UTC", id)
	}
	other, _ := NewEndToEndID("99000001", now)
	if id == other {
		t.Fatal("two identifiers are equal")
	}
}

func TestNewEndToEndIDBadISPB(t *testing.T) {
	for _, ispb := range []string{"", "1234567", "123456789", "abcdefgh"} {
		if _, err := NewEndToEndID(ispb, time.Now()); err == nil {
			t.Errorf("ISPB %q was accepted", ispb)
		}
	}
}

func TestNewRequestID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 50 {
		id, err := NewRequestID()
		if err != nil || !re.MatchString(id) || seen[id] {
			t.Fatalf("id = %q, err = %v", id, err)
		}
		seen[id] = true
	}
}
