package config

import "testing"

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("WALLET_GRPC_ADDR", "")
	t.Setenv("WALLET_ISPB", "")

	c := Load()

	if c.GRPCAddr != ":9090" || c.ISPB != "99000001" {
		t.Fatalf("defaults: got %+v", c)
	}

	t.Setenv("WALLET_ISPB", "12345678")
	if got := Load().ISPB; got != "12345678" {
		t.Fatalf("override: got %q", got)
	}
}
