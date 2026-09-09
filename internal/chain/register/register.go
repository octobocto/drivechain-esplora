// Package register wires every chain decoder into the registry. A command
// imports it for the side effect.
package register

import (
	"github.com/octobocto/drivechain-esplora/internal/chain"
	"github.com/octobocto/drivechain-esplora/internal/chain/bitassets"
	"github.com/octobocto/drivechain-esplora/internal/chain/bitnames"
	"github.com/octobocto/drivechain-esplora/internal/chain/coinshift"
	"github.com/octobocto/drivechain-esplora/internal/chain/photon"
	"github.com/octobocto/drivechain-esplora/internal/chain/thunder"
)

func init() {
	chain.Register(thunder.Decoder{})
	chain.Register(bitassets.Decoder{})
	chain.Register(bitnames.Decoder{})
	chain.Register(coinshift.Decoder{})
	chain.Register(photon.Decoder{})
}
