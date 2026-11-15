package memstore_test

import (
	"testing"

	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/pix/pixtest"
	"pixfake/mock/spi/internal/spi"
	"pixfake/mock/spi/internal/spi/storetest"
)

func TestStore(t *testing.T) {
	storetest.Run(t, func(*testing.T) spi.Store { return memstore.New() })
}

func TestPaymentStore(t *testing.T) {
	pixtest.Run(t, func(*testing.T) pix.PaymentStore { return memstore.New() })
}

func TestAccountStore(t *testing.T) {
	pixtest.RunAccounts(t, func(*testing.T) pix.AccountStore { return memstore.New() })
}

func TestReportStore(t *testing.T) {
	pixtest.RunReports(t, func(*testing.T) pix.ReportStore { return memstore.New() })
}

func TestInspector(t *testing.T) {
	pixtest.RunInspector(t, func(*testing.T) pixtest.Backend { return memstore.New() })
}
