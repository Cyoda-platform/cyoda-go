package memory

import (
	"context"

	spi "github.com/cyoda-platform/cyoda-go-spi"
)

func init() { spi.Register(&plugin{}) }

type plugin struct{}

func (p *plugin) Name() string { return "memory" }

func (p *plugin) NewFactory(
	ctx context.Context,
	getenv func(string) string,
	opts ...spi.FactoryOption,
) (spi.StoreFactory, error) {
	// The memory backend has no configuration and no blocking setup
	// work, so ctx and getenv are unused. Options are also unused
	// (memory doesn't need a ClusterBroadcaster).
	_ = ctx
	_ = getenv
	_ = opts

	factory := newStoreFactory()
	factory.initTransactionManager(&defaultUUIDGenerator{})
	return factory, nil
}
