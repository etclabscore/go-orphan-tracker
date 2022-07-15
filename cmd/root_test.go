package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	mrand "math/rand"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var h1 = `{
  "ID": 0, 
  "CreatedAt": "0001-01-01T00:00:00Z", 
  "UpdatedAt": "0001-01-01T00:00:00Z", 
  "DeletedAt": null, 
  "hash": "0x4438fd87f9c809c411e45a763c6714b32e2531d07907f0ba9c00849dd514ee46", 
  "txes": null, 
  "parentHash": "0x28c50c30baf03d9fa550d02af9846546877e654f9f9fdab4694b6b8f61ff6356", 
  "sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347", 
  "miner": "0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", 
  "stateRoot": "0x2086e806bbbf779d06fd80dd7ffcdd61e40088dd8482ba9e82a7e7ca1aebbd01", 
  "transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421", 
  "receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421", 
  "difficulty": "276942258722463", 
  "number": 15533050, 
  "gasLimit": 8000000, 
  "gasUsed": 0, 
  "timestamp": 1657849659, 
  "extraData": "c3RyYXR1bS1ldS0y", 
  "mixHash": "0x76bb6db7ad194e9833fa139ccaaa4f683d82119aeaafacd57f30f146689f9daf", 
  "nonce": "11772595455916754870", 
  "baseFeePerGas": "\u003cnil\u003e", 
  "orphan": true, 
  "uncleBy": ""
}
`

var h2 = `{
  "ID": 0, 
  "CreatedAt": "0001-01-01T00:00:00Z", 
  "UpdatedAt": "0001-01-01T00:00:00Z", 
  "DeletedAt": null, 
  "hash": "0xb3267f02380623e3ff7f6b77ee60d1cac2c69e56ce6b0befc168178ce1127169", 
  "txes": null, 
  "parentHash": "0xc768249f91f88165a6bb0a9f3c350b0f9495061389d25397c8d9cf4c2b460437", 
  "sha3Uncles": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347", 
  "miner": "0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", 
  "stateRoot": "0x8dda18268a9fda52dccb725cb4288c0bf9cfb7409e4ac7f94fd40ae90f54fa54", 
  "transactionsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421", 
  "receiptsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421", 
  "difficulty": "277887442211848", 
  "number": 15533109, 
  "gasLimit": 8000000, 
  "gasUsed": 0, 
  "timestamp": 1657850361, 
  "extraData": "c3RyYXR1bS1hc2lhLTE=", 
  "mixHash": "0x4d49f15a15dca9c23f6fe6daa7b60f743b3480a8f253958769b22cfbf942dfe5", 
  "nonce": "13059459681546795626", 
  "baseFeePerGas": "\u003cnil\u003e", 
  "orphan": false, 
  "uncleBy": ""
}
`

func randomHex(n int) string {
	bytes := make([]byte, n)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// generateMockHead generates a fake head.
// It DOES NOT FILL the following fields:
// - txes
// - unclehash
// - uncleBy
// - orphan
// - basefee
func generateMockHead() *Head {
	h := &Head{}
	h.Number = uint64(mrand.Int63n(1000000))
	h.Difficulty = fmt.Sprintf("%d", mrand.Int63())

	h.Hash = randomHex(32)
	h.ParentHash = randomHex(32)
	h.Coinbase = randomHex(20)
	h.ReceiptHash = randomHex(32)
	h.TxHash = randomHex(32)
	h.MixDigest = randomHex(32)
	h.Nonce = fmt.Sprintf("%d", mrand.Int63())
	h.Extra = []byte("I was here.")

	h.GasUsed = 63000
	h.GasLimit = 8000000

	return h
}

func TestHeadCreateOrUpdateWithTxes(t *testing.T) {
	testDBPath := filepath.Join(os.TempDir(), "go-orphan-tracker-test-crud1.db")
	os.Remove(testDBPath)

	t.Log(testDBPath)

	db, err := gorm.Open(sqlite.Open(testDBPath), &gorm.Config{})
	if err != nil {
		log.Println(err)
		os.Exit(1)
	}
	db.Debug() // I love verbosity.

	if err := db.AutoMigrate(&Head{}, &Tx{}); err != nil {
		log.Println(err)
		os.Exit(1)
	}

	/*  THIS IS THE PLAN

	1. Create a head.
	2. Create a tx.

	1. Create a head.
	2. Reuse the tx.

	Add head1 as a sidehead.
	Add head2 as a sidehead, demoting head1 to canonical (orphan=false;uncleBy="").
	*/

	head1 := Head{}
	err = json.Unmarshal([]byte(h1), &head1)
	if err != nil {
		t.Fatal(err)
	}

	head2 := Head{}
	err = json.Unmarshal([]byte(h2), &head2)
	if err != nil {
		t.Fatal(err)
	}

	tx1 := Tx{
		Hash:  "0x2338fd47f9c809c411e45a763c6714b32e2531d07907f0ba9c00849dd514ee46",
		From:  "0xDf7D7e053933b5cC24372f878c90E62dADAD5d42",
		To:    "0xDf8D7e053933b5cC24372f878c90E62dADAD5d42",
		Value: "14",
		Nonce: 1,
	}
	tx2 := Tx{
		Hash:  "0xfffffd47f9c809c411e45a763c6714b32e2531d07907f0ba9c00849dd514eeee",
		From:  "0xDf7D7e053933b5cC24372f878c90E62dADAD5d42",
		To:    "0xDf8D7e053933b5cC24372f878c90E62dADAD5d42",
		Value: "17",
		Nonce: 2,
	}

	head1.Txes = []Tx{tx1, tx2}
	head2.Txes = []Tx{tx1, tx2}

	if err := head1.CreateOrUpdate(db, "orphan"); err != nil {
		t.Fatal(err)
	}

	if err := head2.CreateOrUpdate(db, "orphan"); err != nil {
		t.Fatal(err)
	}

	outH1 := Head{}
	db.Model(Head{}).Preload("Txes").First(&outH1)

	j, _ := json.MarshalIndent(outH1, "", "  ")
	t.Log(string(j))

	outH2 := Head{}
	db.Model(Head{}).Preload("Txes").First(&outH2)

	j, _ = json.MarshalIndent(outH2, "", "  ")
	t.Log(string(j))

	if len(head1.Txes) != len(outH1.Txes) {
		t.Fatal("Txes not properly saved")
	}

	if len(head2.Txes) != len(outH2.Txes) {
		t.Fatal("Txes not properly saved")
	}

	if head1.Hash != outH1.Hash {
		t.Fatal("Hash not properly saved")
	}

	if head2.Coinbase != outH2.Coinbase {
		t.Fatal("Coinbase not properly saved")
	}

	h3 := Head{}
	h3.Hash = "0xaaaaad47f9c809c411e45a763c6714b32e2531d07907f0ba9c00849dd514dddd"
	h3.Coinbase = "0xDf7D7e053933b5cC24372f878c90E62dADAD5d43"

	if err := h3.CreateOrUpdate(db, "orphan"); err != nil {
		t.Fatal(err)
	}
}
