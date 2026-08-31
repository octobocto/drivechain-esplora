CREATE TABLE blocks (
    height      INTEGER PRIMARY KEY,
    hash        BYTEA   NOT NULL UNIQUE,
    prev_hash   BYTEA,
    merkle_root BYTEA   NOT NULL,
    main_hash   BYTEA   NOT NULL,
    block_time  BIGINT,
    tx_count    INTEGER NOT NULL
);

CREATE TABLE txs (
    txid       BYTEA   PRIMARY KEY,
    height     INTEGER NOT NULL REFERENCES blocks(height) ON DELETE CASCADE,
    tx_index   INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL,
    fee_sats   BIGINT  NOT NULL,
    raw        BYTEA   NOT NULL
);

CREATE INDEX txs_height_idx ON txs (height DESC, tx_index);

-- outpoint is the node's own 37-byte key: one kind byte, a 32-byte hash, and a
-- little-endian u32.
CREATE TABLE outputs (
    outpoint     BYTEA    PRIMARY KEY,
    kind         SMALLINT NOT NULL,
    source_id    BYTEA    NOT NULL,
    vout         INTEGER  NOT NULL,
    block_hash   BYTEA    NOT NULL,
    address      BYTEA    NOT NULL,
    scripthash   BYTEA    NOT NULL,
    value_sats   BIGINT   NOT NULL,
    content      JSONB    NOT NULL,
    content_type TEXT     NOT NULL,
    height       INTEGER  NOT NULL,
    height_exact BOOLEAN  NOT NULL DEFAULT TRUE,
    spent_source BYTEA,
    spent_kind   SMALLINT,
    spent_vin    INTEGER,
    spent_height INTEGER
);

-- A coinbase outpoint keys on the merkle root, which two blocks with identical
-- contents would share. This catches that collision instead of losing a row.
CREATE UNIQUE INDEX outputs_coinbase_block_idx
    ON outputs (block_hash, vout) WHERE kind = 1;

CREATE INDEX outputs_address_idx     ON outputs (address, height DESC);
CREATE INDEX outputs_unspent_idx     ON outputs (address) WHERE spent_source IS NULL;
CREATE INDEX outputs_scripthash_idx  ON outputs (scripthash, height DESC);
CREATE INDEX outputs_spent_idx       ON outputs (spent_source) WHERE spent_source IS NOT NULL;
CREATE INDEX outputs_source_idx      ON outputs (source_id);
CREATE INDEX outputs_height_idx      ON outputs (height DESC);

-- Deposits key on a mainchain txid, so they get their own lookup.
CREATE INDEX outputs_deposit_idx ON outputs (source_id) WHERE kind = 2;
CREATE INDEX outputs_deposit_height_idx ON outputs (height DESC) WHERE kind = 2;

-- One row. It records how far the index has read.
CREATE TABLE sync_state (
    id           SMALLINT PRIMARY KEY CHECK (id = 1),
    chain        TEXT     NOT NULL,
    network      TEXT     NOT NULL,
    tip_height   INTEGER,
    tip_hash     BYTEA
);
