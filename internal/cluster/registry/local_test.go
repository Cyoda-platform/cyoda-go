package registry_test

import (
	"context"
	"testing"

	"github.com/cyoda-platform/cyoda-go/internal/cluster/registry"
)

func TestLocalRegistry_LookupSelf(t *testing.T) {
	r := registry.NewLocal("node-1", "localhost:8080")
	ctx := context.Background()

	if err := r.Register(ctx, "node-1", "localhost:8080"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	addr, alive, err := r.Lookup(ctx, "node-1")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if !alive {
		t.Error("expected alive=true")
	}
	if addr != "localhost:8080" {
		t.Errorf("addr = %q, want %q", addr, "localhost:8080")
	}
}

func TestLocalRegistry_LookupUnknown(t *testing.T) {
	r := registry.NewLocal("node-1", "localhost:8080")
	ctx := context.Background()

	_, alive, err := r.Lookup(ctx, "node-999")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if alive {
		t.Error("expected alive=false for unknown node")
	}
}

func TestLocalRegistry_ListReturnsSelf(t *testing.T) {
	r := registry.NewLocal("node-1", "localhost:8080")
	ctx := context.Background()

	if err := r.Register(ctx, "node-1", "localhost:8080"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	nodes, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(nodes) != 1 {
		t.Fatalf("len = %d, want 1", len(nodes))
	}
	if nodes[0].NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", nodes[0].NodeID, "node-1")
	}
}
