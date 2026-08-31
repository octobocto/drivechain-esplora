// Package register wires every chain decoder into the registry. A command
// imports it for the side effect.
package register

import (
	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
)

func init() {
	chain.Register(thunder.Decoder{})
}
