package dispatch

import (
	"errors"
	"math/rand"

	"github.com/cyoda-platform/cyoda-go/internal/spi"
)

type PeerSelector interface {
	Select(candidates []spi.NodeInfo) (spi.NodeInfo, error)
}

type RandomSelector struct{}

func NewRandomSelector() *RandomSelector { return &RandomSelector{} }

func (s *RandomSelector) Select(candidates []spi.NodeInfo) (spi.NodeInfo, error) {
	if len(candidates) == 0 {
		return spi.NodeInfo{}, errors.New("no candidates")
	}
	return candidates[rand.Intn(len(candidates))], nil
}
