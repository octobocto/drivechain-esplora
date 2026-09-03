-- The mempool is a snapshot, not a chain. Each pass replaces it whole, so
-- these tables carry no height and never take part in a rollback.

CREATE TABLE mempool_txs (
    txid       BYTEA   PRIMARY KEY,
    tx_index   INTEGER NOT NULL,
    size_bytes INTEGER NOT NULL,
    fee_sats   BIGINT  NOT NULL,
    raw        BYTEA   NOT NULL,
    first_seen BIGINT  NOT NULL
);

CREATE INDEX mempool_txs_seen_idx ON mempool_txs (first_seen DESC);

-- An output an unconfirmed transaction creates. It mirrors outputs, minus
-- every column that only a mined block can fill.
CREATE TABLE mempool_outputs (
    outpoint     BYTEA    PRIMARY KEY,
    kind         SMALLINT NOT NULL,
    source_id    BYTEA    NOT NULL,
    vout         INTEGER  NOT NULL,
    address      BYTEA    NOT NULL,
    scripthash   BYTEA    NOT NULL,
    value_sats   BIGINT   NOT NULL,
    content      JSONB    NOT NULL,
    content_type TEXT     NOT NULL
);

CREATE INDEX mempool_outputs_address_idx    ON mempool_outputs (address);
CREATE INDEX mempool_outputs_scripthash_idx ON mempool_outputs (scripthash);
CREATE INDEX mempool_outputs_source_idx     ON mempool_outputs (source_id);

-- An outpoint an unconfirmed transaction spends. The output it names may be
-- confirmed or may belong to another mempool transaction, so this table holds
-- the outpoint alone and a read joins it either way.
CREATE TABLE mempool_spends (
    outpoint BYTEA    PRIMARY KEY,
    source   BYTEA    NOT NULL,
    kind     SMALLINT NOT NULL,
    vin      INTEGER  NOT NULL
);

CREATE INDEX mempool_spends_source_idx ON mempool_spends (source);
