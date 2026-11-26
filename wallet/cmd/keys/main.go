// Command keys is for operations: it checks that every key in the wallet's copy is still in the DICT, and lists the ones
// that are not. It exits with 1 when it finds any, so it can run from a schedule and raise an alert.
//
//	keys check
//
// The DICT only says whether a key exists, not whose it is: see pixkey.Service.Check for what goes unnoticed.
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
	missing, err := keys.Check(ctx)
	if err != nil {
		slog.Error("check", "err", err)
		os.Exit(1)
	}
	for _, k := range missing {
		fmt.Printf("%s\t%s\t%s\n", k.Key, k.Type, k.AccountID)
	}
	if len(missing) > 0 {
		fmt.Fprintf(os.Stderr, "%d key(s) of the wallet are not in the DICT\n", len(missing))
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "every key of the wallet is in the DICT")
}
