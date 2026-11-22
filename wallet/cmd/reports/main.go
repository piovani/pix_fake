// Command reports is for operations: it lists the book transfers whose report the SPI refused, and puts one back in line to be
// sent again once its cause is fixed.
//
//	reports list
//	reports resend <payment-id>
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

func main() {
	if len(os.Args) < 2 || (os.Args[1] == "resend" && len(os.Args) != 3) || (os.Args[1] != "list" && os.Args[1] != "resend") {
		fmt.Fprintln(os.Stderr, "usage: reports list | reports resend <payment-id>")
		os.Exit(2)
	}
	cfg := config.Load()
	ctx := context.Background()
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to the database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	accounts := ledger.New(pool)
	dict := pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB)
	keys := pixkey.New(accounts, dict, pixkey.NewPostgres(pool), cfg.ISPB)
	payments := payment.New(accounts, keys, dict, pix.NewSPIClient(cfg.SPIBaseURL, cfg.ISPB), payment.NewPostgres(pool), cfg.ISPB)

	switch os.Args[1] {
	case "list":
		refused, err := payments.RejectedReports(ctx)
		if err != nil {
			slog.Error("list", "err", err)
			os.Exit(1)
		}
		for _, p := range refused {
			fmt.Printf("%s\t%s\t%s\t%d\n", p.ID, p.EndToEndID, p.ReportReason, p.AmountCents)
		}
	case "resend":
		ok, err := payments.ResendReport(ctx, os.Args[2])
		if err != nil {
			slog.Error("resend", "err", err)
			os.Exit(1)
		}
		if !ok {
			fmt.Fprintln(os.Stderr, "that payment has no refused report")
			os.Exit(1)
		}
		fmt.Println("the worker will send it again")
	}
}
