package register

import (
	"testing"

	"github.com/octobocto/drivechain-esplora/internal/chain"
)

func TestEveryReleasedChainHasADecoder(t *testing.T) {
	for _, name := range []string{"bitassets", "bitnames", "coinshift", "photon", "thunder", "truthcoin"} {
		spec, decoder, err := chain.Lookup(name)
		if err != nil {
			t.Errorf("lookup %s: %v", name, err)
			continue
		}
		if spec.Name != name || decoder.Name() != name {
			t.Errorf("lookup %s = spec %q, decoder %q", name, spec.Name, decoder.Name())
		}
	}
}
