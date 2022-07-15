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
	"time"

	"github.com/ethereum/go-ethereum/core/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// generateMockHead generates a fake head.
func generateMockHead() *Head {
	h := &Head{}
	h.Number = uint64(mrand.Int63n(1000000))
	h.Difficulty = fmt.Sprintf("%d", mrand.Int63())

	h.Hash = randomHex(32)
	h.ParentHash = randomHex(32)
	h.Time = uint64(time.Now().Unix())
	h.Coinbase = randomHex(20)

	h.ReceiptHash = randomHex(32)
	h.TxHash = randomHex(32)
	h.Root = randomHex(32)
	h.MixDigest = randomHex(32)
	h.Nonce = fmt.Sprintf("%d", mrand.Int63())
	h.Extra = []byte("I was here.")

	h.GasUsed = 63000
	h.GasLimit = 8000000

	h.UncleHash = types.EmptyUncleHash.String()

	// h.Txes = []*Tx{}
	// h.UncleBy = ""
	// h.Orphan = false
	// h.BaseFee = 0

	return h
}

func generateMockTx() Tx {
	tx := Tx{}
	tx.Hash = randomHex(32)
	tx.From = randomHex(20)
	tx.To = randomHex(20)
	tx.Value = fmt.Sprintf("%d", mrand.Int63())
	tx.Nonce = uint64(mrand.Int63())
	return tx
}

func randomHex(n int) string {
	bytes := make([]byte, n)
	rand.Read(bytes)
	return "0x" + hex.EncodeToString(bytes)
}

// TestHeadCreateOrUpdateWithTxes tests the creation of a head with txes.
// In particular, it wants to make sure that the heads_txes join is working
// properly, so we add the same txes to two different heads and save them.
// We want the txes to be "shared" between the two heads, using the hashes
// of each as the foreign keys for the join table.
// We validate a few fields on eventual retrieval of the heads, as well
// as that the proper number of transactions for the queried heads.
func TestHeadCreateOrUpdateWithTxes(t *testing.T) {
	testDBPath := filepath.Join(os.TempDir(), "go-orphan-tracker-test-crud1.db")
	os.Remove(testDBPath) // Clean up on re-run, but leave post-run for inspection.

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

	head1 := generateMockHead()
	head2 := generateMockHead()
	tx1 := generateMockTx()
	tx2 := generateMockTx()

	head1.Txes = []Tx{tx1, tx2}
	head2.Txes = []Tx{tx1, tx2}

	if err := head1.CreateOrUpdate(db, "orphan"); err != nil {
		t.Fatal(err)
	}

	if err := head2.CreateOrUpdate(db, "orphan"); err != nil {
		t.Fatal(err)
	}

	outH1 := Head{}
	db.Model(Head{}).Preload("Txes").Where("hash = ?", head1.Hash).First(&outH1)

	j, _ := json.MarshalIndent(outH1, "", "  ")
	t.Log(string(j))

	outH2 := Head{}
	db.Model(Head{}).Preload("Txes").Where("hash = ?", head2.Hash).First(&outH2)

	j, _ = json.MarshalIndent(outH2, "", "  ")
	t.Log(string(j))

	if len(head1.Txes) != len(outH1.Txes) {
		t.Fatal("Txes not properly saved")
	}

	if len(head2.Txes) != len(outH2.Txes) {
		t.Fatal("Txes not properly saved")
	}

	if head1.Hash != outH1.Hash {
		t.Fatal("Hash not properly saved", head1.Hash, outH1.Hash)
	}

	if head2.Coinbase != outH2.Coinbase {
		t.Fatal("Coinbase not properly saved", head2.Coinbase, outH2.Coinbase)
	}
}
