package chain

import "testing"

// The port arithmetic comes from the orchestrator. A wrong offset dials the
// wrong network's node and indexes the wrong chain.
func TestPorts(t *testing.T) {
	thunder, ok := specs["thunder"]
	if !ok {
		t.Fatal("thunder is missing from the registry")
	}

	cases := []struct {
		network  Network
		wantNode int
		wantAPI  int
	}{
		{Signet, 6009, 3009},
		{Regtest, 16009, 13009},
		{Mainnet, 26009, 23009},
	}

	for _, tc := range cases {
		t.Run(string(tc.network), func(t *testing.T) {
			node, err := thunder.NodeRPCPort(tc.network)
			if err != nil {
				t.Fatalf("node port: %v", err)
			}
			if node != tc.wantNode {
				t.Errorf("node port = %d, want %d", node, tc.wantNode)
			}
			api, err := thunder.APIPort(tc.network)
			if err != nil {
				t.Fatalf("api port: %v", err)
			}
			if api != tc.wantAPI {
				t.Errorf("api port = %d, want %d", api, tc.wantAPI)
			}
		})
	}
}

func TestUnknownNetwork(t *testing.T) {
	if _, err := ParseNetwork("testnet"); err == nil {
		t.Fatal("want an error for an unknown network, got none")
	}
}

// Two chains on one port would make the service index into the wrong database.
func TestPortsDoNotCollide(t *testing.T) {
	seen := map[int]string{}
	for name, spec := range specs {
		for _, port := range []int{spec.NodeBasePort, spec.APIBasePort} {
			if other, ok := seen[port]; ok {
				t.Errorf("chains %q and %q share port %d", name, other, port)
			}
			seen[port] = name
		}
	}
}

func TestLookupUnknownChain(t *testing.T) {
	if _, _, err := Lookup("bitcoin"); err == nil {
		t.Fatal("want an error for an unknown chain, got none")
	}
}
