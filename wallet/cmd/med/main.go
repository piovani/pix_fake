// Command med is for operations: it lists the infraction reports of the MED the wallet is analysing (the worker
// acknowledged them and blocked the money) and closes one with the analysis.
//
//	med list
//	med agree <report-id> <fraud-type> [details]   fraud type: APPLICATION_FRAUD, MULE_ACCOUNT, SCAMMER_ACCOUNT, OTHER
//	med disagree <report-id> [details]             gives the blocked money back
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

const usage = "usage: med list | med agree <report-id> <fraud-type> [details] | med disagree <report-id> [details]"

func main() {
	args := os.Args[1:]
	if len(args) == 0 || (args[0] == "agree" && len(args) < 3) || (args[0] == "disagree" && len(args) < 2) ||
		(args[0] != "list" && args[0] != "agree" && args[0] != "disagree") {
		fmt.Fprintln(os.Stderr, usage)
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

	accounts := ledger.New(pool)
	dict := pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB)
	keys := pixkey.New(accounts, dict, pixkey.NewPostgres(pool), cfg.ISPB)
	payments := payment.New(accounts, keys, dict, pix.NewSPIClient(cfg.SPIBaseURL, cfg.ISPB), payment.NewPostgres(pool), cfg.ISPB)
	fraud := med.New(dict, accounts, payments, med.NewPostgres(pool))

	switch args[0] {
	case "list":
		reports, err := fraud.AwaitingAnalysis(ctx)
		if err != nil {
			slog.Error("list", "err", err)
			os.Exit(1)
		}
		for _, r := range reports {
			fmt.Printf("%s\t%s\t%s\t%d\t%d\t%s\n", r.ID, r.TransactionID, r.Situation, r.AmountCents, r.BlockedCents, r.Details)
		}
	case "agree", "disagree":
		a := med.Analysis{ReportID: args[1], Agree: args[0] == "agree"}
		rest := args[2:]
		if a.Agree {
			a.FraudType, rest = args[2], args[3:]
		}
		a.Details = strings.Join(rest, " ")
		r, err := fraud.Analyse(ctx, a)
		switch {
		case errors.Is(err, med.ErrReportNotFound), errors.Is(err, med.ErrNotAnalysable), errors.Is(err, med.ErrInvalid):
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		case err != nil:
			slog.Error("analyse", "err", err)
			os.Exit(1)
		}
		fmt.Printf("%s\t%s\t%s\t%d\n", r.ID, r.Result, r.BlockStatus, r.BlockedCents)
	}
}
