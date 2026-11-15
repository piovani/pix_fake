package postgres_test

import (
	"context"
	"testing"

	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/pix/pixtest"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/postgres/pgtest"
	"pixfake/mock/spi/internal/spi"
	"pixfake/mock/spi/internal/spi/storetest"
)

func TestStore(t *testing.T) {
	storetest.Run(t, func(t *testing.T) spi.Store {
		return postgres.NewRepository(pgtest.NewPool(t))
	})
}

func TestAddInboundIsAtomic(t *testing.T) {
	repo := postgres.NewRepository(pgtest.NewPool(t))
	err := repo.AddInbound(context.Background(), []spi.Message{
		{ResourceID: "same", ISPB: "11111111", Body: []byte("1")},
		{ResourceID: "same", ISPB: "11111111", Body: []byte("2")},
	})
	if err == nil {
		t.Fatal("repeated ResourceId should fail")
	}
	// Nothing from the failed batch may remain: the first ResourceId is free again.
	if err := repo.AddInbound(context.Background(), []spi.Message{{ResourceID: "same", ISPB: "11111111", Body: []byte("1")}}); err != nil {
		t.Fatalf("batch was not rolled back: %v", err)
	}
}

func TestPaymentStore(t *testing.T) {
	pixtest.Run(t, func(t *testing.T) pix.PaymentStore {
		return postgres.NewRepository(pgtest.NewPool(t))
	})
}
