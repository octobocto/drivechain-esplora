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

// Truthcoin takes slot 13, so its ports follow the same rule as every chain.
func TestTruthcoinPorts(t *testing.T) {
	truthcoin, ok := specs["truthcoin"]
	if !ok {
		t.Fatal("truthcoin is missing from the registry")
	}
	if truthcoin.Slot != 13 {
		t.Errorf("slot = %d, want 13", truthcoin.Slot)
	}
	for network, want := range map[Network][2]int{
		Signet:  {6013, 3013},
		Regtest: {16013, 13013},
		Mainnet: {26013, 23013},
	} {
		node, err := truthcoin.NodeRPCPort(network)
		if err != nil {
			t.Fatalf("%s node port: %v", network, err)
		}
		api, err := truthcoin.APIPort(network)
		if err != nil {
			t.Fatalf("%s api port: %v", network, err)
		}
		if node != want[0] || api != want[1] {
			t.Errorf("%s ports = %d, %d, want %d, %d", network, node, api, want[0], want[1])
		}
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
