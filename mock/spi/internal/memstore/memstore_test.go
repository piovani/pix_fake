package memstore_test

import (
	"testing"

	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/spi"
	"pixfake/mock/spi/internal/spi/storetest"
)

func TestStore(t *testing.T) {
	storetest.Run(t, func(*testing.T) spi.Store { return memstore.New() })
}
