go-orphan-tracker

This program stores non-canonical block and transaction information in a simple relational database.

For all non-canonical blocks discovered (either as sidechained blocks; products of reorgs), or uncle blocks,
the corresponding canonical block and its transactions are also queried and stored.

## Usage

### Build from source

```shell
mkdir -p ./build/bin
go build -o ./build/bin/app .
```

### Run

```shell
mkdir -p data
./build/bin/app --db.path=./data/db.sqlite3 --rpc.target=ws://localhost:8546
```

- `--db.path` is the path to the SQLite database file.
  This file will be created if it does not exist.
  Currently __only sqlite3__ is supported. However, the code can be easily modified to work
  with any database backend supported by the [gorm library](https://gorm.io).

- `--rpc.target` is the target URL of the RPC server (eg. blockchain node client).
  This is the URL that the RPC client will listen on.
  Currently __only websockets or IPC__ are supported, because the program relies on _eth_subscribe_.

## Schema

The Sqlite3 database schema is as follows:

- `heads` This table contains block header information (height, hash, timestamp, etc.).
  It is used to track the sidechain and uncle progress of the blockchain.
  - Entries will fill the boolean `orphan` field as `true` if they are sidechain (non-canonical) blocks.
  - Entries will fill the string `uncle_by` field with the block/header hash of the block/header recording this block as an uncle.
    The field will be empty if the block is not recorded as an uncle.
- `txes` This table contains transactions information (hash, from, to, value, etc.).
  These transactions are contained in either an uncle and/or orphan block.
- `head_txes` This table is a join table which relates the `txes` table to the `heads` table as a many-to-many relation.

## API

- `/ping` This endpoint returns `pong` if the server is running.
- `/api` This endpoint returns all stored block information, with any associated transactions nested.
