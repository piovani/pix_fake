// Command accounts is for operations: it blocks, closes or reopens an account and sets the most it takes in one payment.
//
//	accounts status <account-id> ACTIVE|BLOCKED|CLOSED
//	accounts limit <account-id> <cents>    (0 removes the limit)
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: accounts status <account-id> ACTIVE|BLOCKED|CLOSED | accounts limit <account-id> <cents>")
	os.Exit(2)
}

func main() {
	if len(os.Args) != 4 {
		usage()
	}
	cmd, id, value := os.Args[1], os.Args[2], os.Args[3]

	ctx := context.Background()
	pool, err := db.Connect(ctx, config.Load().DatabaseURL)
	if err != nil {
		slog.Error("connect to the database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	l := ledger.New(pool)

	switch cmd {
	case "status":
		st := ledger.AccountStatus(value)
		if st != ledger.Active && st != ledger.Blocked && st != ledger.Closed {
			usage()
		}
		err = l.SetStatus(ctx, id, st)
	case "limit":
		cents, perr := strconv.ParseInt(value, 10, 64)
		if perr != nil {
			usage()
		}
		err = l.SetReceiveLimit(ctx, id, cents)
	default:
		usage()
	}
	if err != nil {
		slog.Error(cmd, "err", err)
		os.Exit(1)
	}
	fmt.Println("done")
}
