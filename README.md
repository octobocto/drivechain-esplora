# drivechain-esplora

An Esplora-compatible REST API for the rust sidechains.

It walks a sidechain one block at a time, writes an address index into Postgres,
and answers every request from Postgres. The node never scans.

Point an Esplora client at it and the client works with no code change.

## Why

The rust sidechains have no light client. A wallet must run a full node and hold
the whole UTXO set.

The nodes cannot answer an address query at speed. `get_utxos(addresses)` and
`get_stxos(addresses)` do take an arbitrary address set, but both iterate the
whole table and filter in memory. The state database keys by outpoint only. No
chain holds an address index, and some chains have neither method.

So the index belongs outside the node.

## Chains

| Chain | Slot | Node RPC (signet) | This service (signet) |
|---|---|---|---|
| bitnames | 2 | 6002 | 3002 |
| bitassets | 4 | 6004 | 3004 |
| thunder | 9 | 6009 | 3009 |
| truthcoin | 13 | 6013 | 3013 |
| zside | 98 | 6098 | 3098 |
| photon | 99 | 6099 | 3099 |
| coinshift | 255 | 6255 | 3255 |

Regtest adds 10000 to every port. Mainnet adds 20000.

One process and one database per chain. A failure on one chain cannot touch
another.

## Run it

```
docker compose up
curl localhost:3009/blocks/tip/height
```

Or build it:

```
go build ./cmd/drivechain-esplora
./drivechain-esplora --chain thunder --network signet \
    --database-url postgres://localhost/thunder_esplora
```

## The API

The routes and the field names come from Blockstream's Esplora.

```
GET  /blocks/tip/hash          GET /blocks/tip/height
GET  /block/{hash}             GET /block/{hash}/header
GET  /block/{hash}/txids       GET /block/{hash}/txs[/{start_index}]
GET  /block/{hash}/status      GET /block/{hash}/txid/{index}
GET  /block-height/{height}    GET /blocks[/{start_height}]
GET  /tx/{txid}                GET /tx/{txid}/status
GET  /tx/{txid}/hex            GET /tx/{txid}/outspend/{vout}
GET  /tx/{txid}/outspends      POST /tx
GET  /address/{a}              GET /address/{a}/utxo
GET  /address/{a}/txs          GET /address/{a}/txs/chain[/{last_seen}]
GET  /address/{a}/txs/mempool
GET  /scripthash/{h}           and the same four scripthash routes
GET  /fee-estimates
```

### Deposits

A deposit enters a sidechain from the mainchain, so it keys on a mainchain
outpoint and no bitcoin explorer has a route for it. These are new:

```
GET /deposit/{mainchain_txid}    the sidechain outputs that deposit created
GET /deposits[/{start_height}]   recent deposits, newest first
GET /address/{a}/deposits        deposits that paid this address
```

A deposit also shows in `/address/{a}` and `/address/{a}/txs`, like any other
coin.

### Where this differs from bitcoin

Each difference comes from the chain, not from a shortcut.

- **A hash is not reversed.** A sidechain txid is a blake3 digest, and the node
  renders it in plain byte order. Bitcoin reverses its hashes. This service
  keeps the order the chain uses. A mainchain txid inside a deposit stays in
  bitcoin order.
- **There is no script.** An address is a 20-byte ed25519 address.
  `scriptpubkey` carries those 20 bytes as hex, `scriptpubkey_type` reads
  `sidechain_address`, and `scriptpubkey_address` carries the base58 form. The
  `/scripthash/` routes key on the sha256 of the address bytes.
- **There is no fee market.** `/fee-estimates` answers with one constant rather
  than an error, because a client calls it before every send.
- **A withdrawal costs more than it pays.** A withdrawal output removes both its
  payout and its mainchain fee from the sidechain, because the enforcer pays
  both out of the treasury. The index counts both.
- **A coinbase output belongs to the block, not to a transaction.** It keys on
  the header merkle root.
- **`version`, `locktime` and `sequence` are always 0.** These chains have no
  such fields. The API still carries them, so a client parser does not break.

## What the node must serve

This service reads three methods every rust sidechain already has:
`get_best_sidechain_block_hash`, `getblockcount`, and `get_block`.

It reads two more that are new:

- `get_block_hash(height)` returns the hash at one height. The function already
  exists inside the node; only the RPC is missing.
- `get_block_index(block_hash)` returns what a block body does not carry: each
  transaction's txid and canonical size, the mainchain deposits, and the outputs
  a withdrawal bundle removed.

The second one is necessary, not a convenience. A deposit never appears in a
block body, and a withdrawal bundle spends outputs with no transaction at all.
An index built from block bodies alone misses both.

A node that adds the deposit index resyncs one time to record historic deposits.
Every deposit after that is exact.

## Licence

MIT.
