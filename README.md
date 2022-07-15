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

- `headers` This table contains block header information (height, hash, timestamp, etc.).
  It is used to track the sidechain and uncle progress of the blockchain.
  - Entries will fill the boolean `orphan` field as `true` if they are sidechain (non-canonical) blocks.
  - Entries will fill the string `uncleBy` field with the block/header hash of the block/header recording this block as an uncle.
    The field will be empty if the block is not recorded as an uncle.
- `txes` This table contains transactions information (hash, from, to, value, etc.).
  These transactions are contained in either an uncle and/or orphan block.
- `header_txes` This table is a join table which relates the `txes` table to the `headers` table as a many-to-many relation.

Fields which are natively `common.Hash` or `common.Address` or `*big.Int` or other "specialty" fields (`BlockNonce`) are coerced to (usually) `string` or sometimes `uint64` if I'm sure they won't overflow. `common.Hash` and `common.Address` values will be stored hex-encoded, while `*big.Int` values are stored as numerical strings (via the `*big.Int.String()` method). 

![image](https://user-images.githubusercontent.com/45600330/179063477-56d21c7b-55e5-470c-8d69-433dc8f8f3e8.png)

## API

There is a live server running this program at [classic.orphans.etccore.in](https://classic.orphans.etccore.in/api).

### Examples

- [https://classic.orphans.etccore.in/ping](https://classic.orphans.etccore.in/ping)
- [https://classic.orphans.etccore.in/status](https://classic.orphans.etccore.in/status)
- [https://classic.orphans.etccore.in/api/headers](https://classic.orphans.etccore.in/api/headers)
- [https://classic.orphans.etccore.in/api/headers?orphan_only=true](https://classic.orphans.etccore.in/api/headers?orphan_only=true)
- [https://classic.orphans.etccore.in/api/headers?orphan_only=true&include_txes=false](https://classic.orphans.etccore.in/api/headers?orphan_only=true&include_txes=false)
- [https://classic.orphans.etccore.in/api/headers?orphan_only=true&include_txes=false&limit=1&offset=1](https://classic.orphans.etccore.in/api/headers?orphan_only=true&include_txes=false&limit=1&offset=1)
- [https://classic.orphans.etccore.in/api/headers?raw_sql=SELECT * FROM headers WHERE number > 15537020 AND number < 15537055 AND orphan == true](https://classic.orphans.etccore.in/api/headers?raw_sql=SELECT%20*%20FROM%20headers%20WHERE%20number%20%3E%2015537020%20AND%20number%20%3C%2015537055%20AND%20orphan%20==%20true)

### Endpoints

#### `/ping` 

This endpoint returns `pong` if the server is running.

#### `/status` 

This endpoint returns the current status of the server, including uptime and latest block.

Example response:

```json
{
  "uptime": 324,
  "chain_id": 61,
  "latest_header": {
        "created_at": "0001-01-01T00:00:00Z",
        "updated_at": "0001-01-01T00:00:00Z",
        "hash": "0x4018a7851f87ac7c7c7da1549aa11717979acaaef8937e67b1db3a573e5df29a",
        "parentHash": "0x742fe6c7bb519a9209fb1ab4a69e9133b34b7926bebd62b100033f6f60ed89e4",
        "sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
        "miner": "0xDf7D7e053933b5cC24372f878c90E62dADAD5d42",
        "stateRoot": "0xf9df79e74c9f87a3774bdc52ece20837314e9579f831006a85c23adbe16a32d9",
        "transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
        "receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
        "difficulty": "267018370939767",
        "number": 15536588,
        "gasLimit": 8031275,
        "gasUsed": 0,
        "timestamp": 1657896534,
        "extraData": "c3RyYXR1bS1hc2lhLTE=",
        "mixHash": "0x5e7b903556dcaa4a738152830194044b9a94f1ccf189a98146e5f66af81c96ca",
        "nonce": "14687018096225711779",
        "baseFeePerGas": "<nil>",
        "orphan": false,
        "uncleBy": ""
    }
}
```

#### `/api/headers` 

This endpoint returns all stored block information, with any associated transactions nested. The default behavior will return all blocks and their transactions nested, and the blocks will be in descending order by number.

__Kitchen Sink example:__ [https://classic.orphans.etccore.in/api/headers?limit=1&offset=1&orphan_only=true&include_txes=false](https://classic.orphans.etccore.in/api?limit=1&offset=1&orphan_only=true&include_txes=false)

##### Query Parameters
  
- `limit` This query parameter limits the number of blocks returned. Its value should be an integer. Default is `1000`.

- `offset` This query parameter offsets the blocks returned. Its value should be an integer. Default is `0`.

- `orphan_only` This query parameter returns only orphan blocks. Its value should be a boolean. Default is `false`.
  
- `include_txes` This query parameter enables/disables the inclusion of transactions in the response. Transactions are included by default. To disable, use `?include_txes=false`. 

- `number_min`, `number_max` These query parameters limit the blocks returned to those with a header number between the min and max values. The values should be integers, and will be inclusive bounds.

- `raw_sql` This query parameter enables the caller to execute arbitrary SQL queries, eg.

  Live demo example: [https://classic.orphans.etccore.in/api/headers?raw_sql=SELECT * FROM headers WHERE number > 15537020 AND number < 15537055 AND orphan == true](https://classic.orphans.etccore.in/api?raw_sql=SELECT%20*%20FROM%20heads%20WHERE%20number%20%3E%2015537020%20AND%20number%20%3C%2015537055%20AND%20orphan%20==%20true)

  :warning: This query parameter precludes any other query parameters. Any other query parameters will be ignored.

![image](https://user-images.githubusercontent.com/45600330/179065843-e8eec559-ba8a-415c-b24d-67d0bf49bfed.png)

#### `/api/txes`

This endpoint returns transaction information. Blocks may be nested under transactions with the annotation `headers`.

##### Query Parameters

- `limit` This query parameter limits the number of transactions returned. Its value should be an integer. Default is `1000`.

- `offset` This query parameter offsets the transactions returned. Its value should be an integer. Default is `0`.

- `include_headers` This query parameter enables/disables the inclusion of related headers in the response. Headers are included by default. To disable, use `?include_headers=false`. 

- `raw_sql` This query parameter enables the caller to execute arbitrary SQL queries.
  :warning: This query parameter precludes any other query parameters. Any other query parameters will be ignored.
