// Command keys is for operations: it reconciles the wallet's keys with the DICT (sync verification by key type, then the
// DICT's file of CIDs where they differ) and lists each key that disagrees, as kind, key, type and account:
//
//	MISSING_IN_DICT  the wallet has it, the DICT does not
//	DATA_DIFFERS     both have it, with other account, holder or RequestId
//	ONLY_IN_DICT     the DICT has it under the wallet, the wallet does not (no account)
//
// It exits with 1 when it finds any, so it can run from a schedule and raise an alert.
//
//	keys check
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "check" {
		fmt.Fprintln(os.Stderr, "usage: keys check")
		os.Exit(2)
	}
	cfg := config.Load()
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	keys := pixkey.New(ledger.New(pool), pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB), pixkey.NewPostgres(pool), cfg.ISPB)
	findings, err := keys.Check(ctx)
	if err != nil {
		slog.Error("check", "err", err)
		os.Exit(1)
	}
	for _, f := range findings {
		fmt.Printf("%s\t%s\t%s\t%s\n", f.Kind, f.Key, f.Type, f.AccountID)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "%d key(s) disagree with the DICT\n", len(findings))
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "the wallet's keys and the DICT agree")
}
