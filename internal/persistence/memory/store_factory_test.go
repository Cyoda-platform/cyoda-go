package memory_test

import (
	"context"
	"testing"

	spi "github.com/cyoda-platform/cyoda-go-spi"

	"github.com/cyoda-platform/cyoda-go/internal/common"
	"github.com/cyoda-platform/cyoda-go/internal/persistence/memory"
)

func TestStoreFactory_TransactionManagerMethod(t *testing.T) {
	f := memory.NewStoreFactory()
	// Must be pre-initialized for the memory factory to return a TM.
	f.NewTransactionManager(common.NewDefaultUUIDGenerator())

	tm, err := f.TransactionManager(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tm == nil {
		t.Fatal("expected non-nil TransactionManager")
	}
	// Factory's type must satisfy spi.StoreFactory with the new method.
	var _ spi.StoreFactory = f
}
