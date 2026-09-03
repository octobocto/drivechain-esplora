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
GET  /mempool                  GET /mempool/txids
GET  /mempool/recent           GET /fee-estimates
```

### Unconfirmed coins

A wallet must see a payment before a block carries it. On thunder a block takes
minutes, so an index that serves mined blocks alone shows a user nothing at all
for that time.

So the index holds the unconfirmed set too. It reads `get_block_template` four
times a second, which is the block the node would mine next, and that body is
the node's mempool. A pass replaces the whole snapshot, so a transaction the
node dropped leaves the index with it.

The snapshot reaches every route a wallet reads:

- `/address/{a}` fills `mempool_stats`.
- `/address/{a}/utxo` adds the unconfirmed coins, and drops a confirmed coin
  the mempool spends. A spend leaves the balance at once.
- `/address/{a}/txs` carries the unconfirmed rows first, and
  `/address/{a}/txs/mempool` carries them alone.
- `/tx/{txid}` finds an unconfirmed transaction and answers
  `"confirmed": false`.
- `/mempool`, `/mempool/txids` and `/mempool/recent` count the whole set.

A block template names no txid and no size, so the index computes both from the
transaction itself: a blake3 digest over the borsh encoding, which is what the
node does.

### Drivechain

A sidechain lives inside the mainchain's hashrate escrow, and only the mainchain
knows which slots activated or what each treasury holds. These routes read that,
so a wallet with no local node still sees it:

```
GET /drivechain/sidechains        every activated slot, in slot order
GET /drivechain/sidechain/{slot}  one slot
```

Each answers the slot, the title and description from its M1 declaration, the
vote count, the proposal and activation heights, and the treasury. A slot the
mainchain holds no treasury for answers `"treasury": null`, never zero sats, so
a caller can tell "nothing deposited yet" from "a treasury holding nothing".

The treasury carries the CTIP, which is the outpoint a deposit spends. A wallet
needs it to build an M5.

These routes read through to a bip300301 enforcer named by `--enforcer-url`,
rather than indexing the mainchain here. They are therefore only as available as
that enforcer. A deployment with no enforcer serves every other route and
answers 503 on these two.

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
- **`POST /tx` takes JSON, not hex.** Bitcoin posts a hex raw transaction. These
  chains have no such form: a node reads an authorized transaction as JSON, and
  almost none of its types can borsh-decode. So the body is that JSON, and this
  service relays it to the node unchanged. It is the one write route, and it
  neither signs nor rewrites what it carries.
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

This service reads four methods every rust sidechain already has:
`get_best_sidechain_block_hash`, `getblockcount`, `get_block`, and
`get_block_template`.

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

`get_block_template` needs a mainchain connection, because the header it builds
names the mainchain tip. A node with no enforcer behind it serves every mined
block and no unconfirmed set.

## Licence

MIT.
