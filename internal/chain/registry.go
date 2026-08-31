package chain

import (
	"fmt"
	"sort"
)

// Network selects the port band a node listens on.
type Network string

const (
	Signet  Network = "signet"
	Regtest Network = "regtest"
	Mainnet Network = "mainnet"
)

// portOffset is what a network adds to every base port. The rust sidechains
// share this table.
func (n Network) portOffset() (int, error) {
	switch n {
	case Signet:
		return 0, nil
	case Regtest:
		return 10000, nil
	case Mainnet:
		return 20000, nil
	default:
		return 0, fmt.Errorf("unknown network %q", n)
	}
}

// ParseNetwork reads a network name.
func ParseNetwork(s string) (Network, error) {
	n := Network(s)
	if _, err := n.portOffset(); err != nil {
		return "", err
	}
	return n, nil
}

// Spec describes one rust sidechain.
type Spec struct {
	// Name is the chain name, as the chain registry spells it.
	Name string
	// Slot is the BIP300 sidechain slot.
	Slot int
	// NodeBasePort is the node's signet JSON-RPC port.
	NodeBasePort int
	// APIBasePort is this service's signet listen port.
	APIBasePort int
}

// NodeRPCPort is the node's JSON-RPC port on one network.
func (s Spec) NodeRPCPort(n Network) (int, error) {
	offset, err := n.portOffset()
	if err != nil {
		return 0, err
	}
	return s.NodeBasePort + offset, nil
}

// APIPort is this service's listen port on one network.
func (s Spec) APIPort(n Network) (int, error) {
	offset, err := n.portOffset()
	if err != nil {
		return 0, err
	}
	return s.APIBasePort + offset, nil
}

// specs holds every rust sidechain. The node ports come from the orchestrator's
// chain registry. This service takes the 3000 band, which nothing else uses.
var specs = map[string]Spec{
	"bitnames":  {Name: "bitnames", Slot: 2, NodeBasePort: 6002, APIBasePort: 3002},
	"bitassets": {Name: "bitassets", Slot: 4, NodeBasePort: 6004, APIBasePort: 3004},
	"thunder":   {Name: "thunder", Slot: 9, NodeBasePort: 6009, APIBasePort: 3009},
	"truthcoin": {Name: "truthcoin", Slot: 13, NodeBasePort: 6013, APIBasePort: 3013},
	"zside":     {Name: "zside", Slot: 98, NodeBasePort: 6098, APIBasePort: 3098},
	"photon":    {Name: "photon", Slot: 99, NodeBasePort: 6099, APIBasePort: 3099},
	"coinshift": {Name: "coinshift", Slot: 255, NodeBasePort: 6255, APIBasePort: 3255},
}

// decoders holds the chains this service can read. A chain gains an entry when
// its output payload decoder lands.
var decoders = map[string]Decoder{}

// Register adds a decoder for one chain. It panics on an unknown chain name or
// a second registration, because both are build-time mistakes.
func Register(d Decoder) {
	name := d.Name()
	if _, ok := specs[name]; !ok {
		panic(fmt.Sprintf("chain %q is not in the registry", name))
	}
	if _, ok := decoders[name]; ok {
		panic(fmt.Sprintf("chain %q already has a decoder", name))
	}
	decoders[name] = d
}

// Lookup returns the spec and decoder for one chain.
func Lookup(name string) (Spec, Decoder, error) {
	spec, ok := specs[name]
	if !ok {
		return Spec{}, nil, fmt.Errorf("unknown chain %q, want one of %v", name, Names())
	}
	decoder, ok := decoders[name]
	if !ok {
		return Spec{}, nil, fmt.Errorf(
			"chain %q has no output decoder yet, want one of %v", name, Supported())
	}
	return spec, decoder, nil
}

// Names lists every rust sidechain in the registry.
func Names() []string { return sortedKeys(specs) }

// Supported lists the chains this service can read today.
func Supported() []string { return sortedKeys(decoders) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
