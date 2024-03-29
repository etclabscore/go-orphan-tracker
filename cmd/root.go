/*
Package cmd

Copyright © 2022 Isaac

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm/clause"

	"github.com/gorilla/handlers"
	"gorm.io/gorm"
)

var cfgFile string
var rpcTarget string
var dbPath string
var httpAddr string
var chainID *big.Int

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.go-orphan-tracker.yaml)")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	rootCmd.Flags().StringVar(&rpcTarget, "rpc.target", "", "RPC target endpoint, eg. /path/to/geth.ipc")
	rootCmd.Flags().StringVar(&dbPath, "db.path", "", "Path to database file, eg. /path/to/db.sqlite")
	rootCmd.Flags().StringVar(&httpAddr, "http.addr", ":8080", "Address to serve HTTP API on, eg. :8080")

}

// Header is our app representation of a block header.
// We have to reinvent the wheel because we want to play nice with the database,
// and the database doesn't have a model *big.Ints or common.Hash or block.Nonce, etc.
// All *big.Ints are stored as strings in the database unless they are safely converted to uint64s (ie block number).
// All common.Hashes are stored as strings.
type Header struct {

	// These field are taken from gorm.Model, but omitting the ID field. We'll use Hash instead.
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	// Block is a pointer to the block this header belongs to.
	// We'll need to this from the server.
	Block *types.Block `json:"-" gorm:"-"`

	// Hash is the SAME VALUE as Header.Hash(), but we get to tell gorm that it must be unique.
	Hash string `gorm:"unique;index;primaryKey;" json:"hash"`

	/*
		> https://gorm.io/docs/many_to_many.html#Override-Foreign-Key

		type User struct {
		  gorm.Model
		  Profiles []Profile `gorm:"many2many:user_profiles;foreignKey:Refer;joinForeignKey:UserReferID;References:UserRefer;joinReferences:ProfileRefer"`
		  Refer    uint      `gorm:"index:,unique"`
		}

		type Profile struct {
		  gorm.Model
		  Name      string
		  UserRefer uint `gorm:"index:,unique"`
		}

		// Which creates join table: user_profiles
		//   foreign key: user_refer_id, reference: users.refer
		//   foreign key: profile_refer, reference: profiles.user_refer
	*/
	Txes []Tx `gorm:"many2many:header_txes;foreignKey:Hash;references:Hash" json:"txes,omitempty"`

	// types.Header:
	ParentHash  string `json:"parentHash"`
	UncleHash   string `json:"sha3Uncles"`
	Coinbase    string `json:"miner"`
	Root        string `json:"stateRoot"`
	TxHash      string `json:"transactionsRoot" gorm:"column:txes_root"`
	ReceiptHash string `json:"receiptsRoot"`
	Difficulty  string `json:"difficulty"`
	Number      uint64 `json:"number"`
	GasLimit    uint64 `json:"gasLimit"`
	GasUsed     uint64 `json:"gasUsed"`
	Time        uint64 `json:"timestamp"`
	Extra       []byte `json:"extraData"`
	MixDigest   string `json:"mixHash"`
	Nonce       string `json:"nonce"`
	BaseFee     string `json:"baseFeePerGas,omitempty"` // BaseFee was added by EIP-1559 and is ignored in legacy headers.

	// Uncle1 and Uncle2 are optionally filled fields.
	// The Ethereum protocol only allows blocks to cite 2 uncles at most.
	Uncle1 string `json:"uncle1,omitempty"`
	Uncle2 string `json:"uncle2,omitempty"`

	// Orphan is a flag indicating whether this header is an orphan.
	Orphan bool `gorm:"default:false" json:"orphan"`

	// UncleBy is the hash of the block/header listing this uncle as an uncle.
	// If empty, it was not recorded as an uncle.
	UncleBy string `json:"uncleBy"`

	// Error describes any error that took place while fetching/filling/handling this header.
	// Errors could be from fetching the block (to get the transactions), for example.
	// We persist errors because it is most important to us that we store
	// all block records. We should not abort saving if a non-critical errors occurrs
	// along the way. Better to save a header without the transactions, but with the error,
	// than to save no header at all.
	Error string `json:"error"`
}

type Tx struct {
	// These field are taken from gorm.Model, but omitting the ID field. We'll use Hash instead.
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Hash string `json:"hash" gorm:"unique;index;primaryKey"`

	Headers []*Header `gorm:"many2many:header_txes;foreignKey:Hash;references:Hash" json:"headers,omitempty"`

	From     string `json:"from"`
	To       string `json:"to"`
	Data     string `json:"data"`
	GasPrice string `json:"gasPrice"`
	GasLimit string `json:"gasLimit"`
	Value    string `json:"value"`
	Nonce    uint64 `json:"nonce"`
}

// type HeadTx struct {
// 	HeadHash  string `json:"head_hash" gorm:"primaryKey"`
// 	TxHash    string `json:"tx_hash" gorm:"primaryKey"`
// 	CreatedAt time.Time
// 	DeletedAt gorm.DeletedAt
// }

// appHeader translates the original header into a our app specific header struct type.
func appHeader(header *types.Header) *Header {
	nonce, _ := header.Nonce.MarshalText()
	h := &Header{
		Hash:        header.Hash().Hex(),
		ParentHash:  header.ParentHash.Hex(),
		UncleHash:   header.UncleHash.Hex(),
		Coinbase:    header.Coinbase.Hex(),
		Root:        header.Root.Hex(),
		TxHash:      header.TxHash.Hex(),
		ReceiptHash: header.ReceiptHash.Hex(),
		Difficulty:  (*hexutil.Big)(header.Difficulty).String(),
		Number:      header.Number.Uint64(),
		GasLimit:    header.GasLimit,
		GasUsed:     header.GasUsed,
		Time:        header.Time,
		Extra:       header.Extra,
		MixDigest:   header.MixDigest.Hex(),
		Nonce:       string(nonce),
		// Orphan
		// UncleBy
	}

	if header.BaseFee != nil {
		h.BaseFee = header.BaseFee.String()
	}

	return h
}

// CreateOrUpdate creates or updates a header, returning any error.
// assignCols should be any of "uncle" or "orphan"; these are the fields which
// are permitted to be updated in case the record already exists.
func (h *Header) CreateOrUpdate(db *gorm.DB, assignCols ...string) error {
	cols := []string{}
	cols = append(cols, assignCols...)
	res := db.
		// Session(&gorm.Session{FullSaveAssociations: true}).
		Clauses(
			clause.OnConflict{
				Columns:   []clause.Column{{Table: "headers", Name: "hash"}},
				DoUpdates: clause.AssignmentColumns(cols),
				// UpdateAll: true,
			},
			// clause.OnConflict{
			// 	Columns:   []clause.Column{{Table: "tx", Name: "hash"}},
			// 	UpdateAll: true,
			// },
		).Create(h)

	if res.Error != nil {
		return res.Error
	}

	if h.Txes == nil || len(h.Txes) == 0 {
		return nil
	}

	for txi, tx := range h.Txes {
		tx.Headers = []*Header{h}
		h.Txes[txi] = tx
	}

	res = db.Clauses(
		clause.OnConflict{
			Columns:   []clause.Column{{Table: "txes", Name: "hash"}},
			UpdateAll: true,
		},
	).Create(&h.Txes)

	return res.Error
}

func appTx(tx *types.Transaction, baseFee *big.Int) (Tx, error) {
	to := ""
	if tx.To() != nil {
		to = tx.To().Hex()
	}

	msg, err := tx.AsMessage(types.NewEIP2930Signer(chainID), baseFee)
	if err != nil {
		return Tx{}, err
	}

	return Tx{
		From:     msg.From().Hex(),
		To:       to,
		Data:     common.Bytes2Hex(tx.Data()),
		GasPrice: tx.GasPrice().String(),
		GasLimit: tx.GasFeeCap().String(),
		Value:    tx.Value().String(),
		Nonce:    tx.Nonce(),
		Hash:     tx.Hash().Hex(),
	}, nil
}

func blockTxes2AppTxes(blTxes []*types.Transaction, blBaseFee *big.Int) ([]Tx, error) {
	headerTxes := []Tx{}
	for _, tx := range blTxes {
		tx, err := appTx(tx, blBaseFee)
		if err != nil {
			return headerTxes, err
		}
		headerTxes = append(headerTxes, tx)
	}
	return headerTxes, nil
}

func handleHeader(client *ethclient.Client, db *gorm.DB, tHeader *types.Header, isOrphan bool, uncleBy string) (*Header, error) {
	header := appHeader(tHeader)

	header.Orphan = isOrphan
	header.UncleBy = uncleBy

	bl, err := client.BlockByHash(context.Background(), common.HexToHash(header.Hash))
	if err != nil {
		return nil, err
	}

	// Hold the queried block in mem just in case.
	header.Block = bl

	header.Txes, err = blockTxes2AppTxes(bl.Transactions(), bl.BaseFee())
	if err != nil {
		return header, err
	}

	for i, uncle := range bl.Uncles() {
		if i == 0 {
			header.Uncle1 = uncle.Hash().Hex()
		} else {
			header.Uncle2 = uncle.Hash().Hex()
		}
		if _, err := handleHeader(client, db, uncle, true, header.Hash); err != nil {
			return nil, err
		}
	}

	assignCols := []string{"orphan"}
	if uncleBy != "" {
		assignCols = append(assignCols, "uncle_by")
	}

	err = header.CreateOrUpdate(db, assignCols...)
	if err != nil {
		return nil, err
	}

	// This is a canonical block.
	// Any other blocks at this height are orphans.
	if !isOrphan {
		db.Model(&Header{}).
			Where("number = ?", header.Number).
			Where("hash != ?", header.Hash).
			Update("orphan", true)
	}

	return header, nil
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "go-orphan-tracker",
	Short: "A program to record orphan (non-canonical) ETH/ETC blocks",
	Long: `This program creates a database of orphan blocks and their canonical counterparts.

This program demands the configured RPC endpoint to support subscriptions; either a Websocket or IPC endpoint must be used.
*** RPC HTTP transport is not supported. ***

eth_subscribeNewSideHeads is used to subscribe to new side block events.
*** ONLY github.com/etclabscore/core-geth supports this API method. ***

When a new side block event happens, the reported side block is recorded in the database.
Its canonical counterpart is queried via eth_getHeaderByNumber and that header too is stored in the database.

eth_subscribeNewHeads is used to subscribe to new blocks.
These new (canonical) blocks are audited to see if they have listed uncles.
If not, they're only held in memory as the latest block to give an observable status to the program.
If they DO contain uncles, we query for those uncles and record them, along with
the canonical block which cites them (ie. the current head).
`,
	// Uncomment the following line if your bare application
	// has an action associated with it:
	Run: func(cmd *cobra.Command, args []string) {

		// Set up the RPC connection
		// --------------------------------------------------
		if rpcTarget == "" {
			log.Println("Please specify an RPC target")
			os.Exit(1)
		}

		rpcClient, err := rpc.Dial(rpcTarget)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		client := ethclient.NewClient(rpcClient)
		log.Println("Connected client to RPC target", rpcTarget)

		// Get the chainID and store in mem because we need it for transaction signer extraction.
		chainID, err = client.ChainID(context.Background())
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		log.Println("Chain ID:", chainID)

		latestH, err := client.HeaderByNumber(context.Background(), nil)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		statusLatestHead = appHeader(latestH)

		// Set up the database
		// --------------------------------------------------
		if dbPath == "" {
			log.Println("Please specify a database path")
			os.Exit(1)
		}

		db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}
		db.Debug() // I love verbosity.

		if err := db.AutoMigrate(&Header{}, &Tx{}); err != nil {
			log.Println(err)
			os.Exit(1)
		}

		// Set up the subscriptions and channels
		// --------------------------------------------------
		quitCh := make(chan os.Signal, 10)

		interruptCh := make(chan os.Signal, 1)
		signal.Notify(interruptCh, os.Interrupt, os.Kill)

		var sideSub, headSub ethereum.Subscription
		sideHeadCh, headCh := make(chan *types.Header, 10_000), make(chan *types.Header, 10_000)

		setupClientSubsctription := func(sub string) (err error) {
			switch sub {
			case "head":
				headSub, err = client.SubscribeNewHead(context.Background(), headCh)
			case "side":
				sideSub, err = client.SubscribeNewSideHead(context.Background(), sideHeadCh)
			default:
				panic("Unknown subscription type")
			}
			return err
		}

		err = setupClientSubsctription("side")
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		err = setupClientSubsctription("head")
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		// trailCh will be our channel to signal events
		// for a process that trails the current latest block by
		// some constant height.
		trailerCh := make(chan *types.Header, 10_000)
		const trailHeight = uint64(10)

		// Run the main loop.
		// --------------------------------------------------
		go func() {
			for {
				select {
				// Shutdown
				// --------------------------------------------------
				case sig := <-interruptCh:
					log.Println("Received signal:", sig)
					quitCh <- sig
					return

					// Errors
					// --------------------------------------------------
				case err := <-sideSub.Err():
					log.Println(err)
					if strings.Contains(strings.ToLower(err.Error()), "connection") {
						subErr := setupClientSubsctription("side")
						if subErr != nil {
							log.Println(subErr)
							quitCh <- os.Interrupt
							return
						}
						continue
					}
					quitCh <- os.Interrupt
					return

				case err := <-headSub.Err():
					log.Println(err)
					if strings.Contains(strings.ToLower(err.Error()), "connection") {
						subErr := setupClientSubsctription("head")
						if subErr != nil {
							log.Println(subErr)
							quitCh <- os.Interrupt
							return
						}
						continue
					}
					quitCh <- os.Interrupt
					return

					// Sides
					// --------------------------------------------------
					// Any blocks that come through this channel should be stored.
				case header := <-sideHeadCh:

					sideHead, err := handleHeader(client, db, header, true, "")
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}
					log.Println("New side head:", headerStr(sideHead))

					// Now query and store the block by number to get the canonical headers corresponding to
					// this uncle by height.
					canonBlock, err := client.BlockByNumber(context.Background(), header.Number)
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					_, err = handleHeader(client, db, canonBlock.Header(), false, "")
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					// Canons
					// --------------------------------------------------
					// Only some blocks that come through this channel should be stored.
					// We want to store blocks that are RELATED, somehow, to orphan blocks.
					// These relations can be as:
					// - competitor blocks by height
					// - uncling blocks, which include orphan references
				case header := <-headCh:

					latestHead := appHeader(header)

					// Overwrite any existing row by number with orphan=true.
					// We ignore any error because we don't care if there are no matching entries in the db
					// and this tx will be a noop.
					db.Model(&Header{}).
						Where("number = ?", header.Number.Uint64()).
						Where("hash != ?", header.Hash().Hex()).
						Update("orphan", true)

					// Flag a conflict at the current head block.
					// Any events resulting in a conflict will cause the block
					// to be stored, just in case.
					conflict := latestHead.Number == statusLatestHead.Number &&
						latestHead.Hash != statusLatestHead.Hash
					conflict = conflict || latestHead.Number < statusLatestHead.Number
					conflict = conflict || latestHead.ParentHash != statusLatestHead.Hash

					// Fire this new header off to the trailer channel.
					trailerCh <- header

					// Update the in-mem latest head value that's used for the server status.
					statusLatestHead = latestHead
					log.Println("New head:", headerStr(latestHead))

					if header.UncleHash == types.EmptyUncleHash && !conflict {
						continue
					}

					_, err = handleHeader(client, db, header, false, "")
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					// Trailer
					// --------------------------------------------------
				case header := <-trailerCh:
					trailerHeight := header.Number.Uint64() - trailHeight

					storedHeaders := []*Header{}
					err := db.Model(&Header{}).
						Where("number = ?", trailerHeight).
						Find(&storedHeaders).Error

					if err != nil && err != gorm.ErrRecordNotFound {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}
					if err == gorm.ErrRecordNotFound || len(storedHeaders) == 0 {
						continue // Noop. We have no stored block data for this height.
					}

					countCanonical := 0
					for _, header := range storedHeaders {
						if !header.Orphan {
							countCanonical++
						}
					}
					if countCanonical < 1 || countCanonical > 1 {
						// Fetch the canonical block by height.
						canonBlock, err := client.BlockByNumber(context.Background(), big.NewInt(int64(trailHeight)))
						if err != nil {
							log.Println(err)
							quitCh <- os.Interrupt
							return
						}

						_, err = handleHeader(client, db, canonBlock.Header(), false, "")
						if err != nil {
							log.Println(err)
							quitCh <- os.Interrupt
							return
						}
					}
				}
			}
		}()

		// Start the HTTP API.
		// --------------------------------------------------
		httpServerExitDone := &sync.WaitGroup{}
		httpServerExitDone.Add(1)
		srv := startHttpServer(httpServerExitDone, db)

		// Block for user interrupt or error.
		// --------------------------------------------------
		<-quitCh

		// Initiate shutdown.
		// --------------------------------------------------
		log.Println("Shutting down...")

		// Now close the server gracefully ("shutdown").
		shutdownCtx, _ := context.WithTimeout(context.Background(), time.Second*10)
		if err := srv.Shutdown(shutdownCtx); err != nil {

			// Failure/timeout shutting down the server gracefully.
			panic(err)
		}

		// Wait for goroutine started in startHttpServer() to stop.
		httpServerExitDone.Wait()

		log.Println("Server shutdown complete")

		sideSub.Unsubscribe()
		headSub.Unsubscribe()

		log.Println("Subscriptions closed")
	},
}

func headerStr(header *Header) string {

	// j, _ := json.Marshal(header)
	// return string(j)

	hasUncles := "no"
	if common.HexToHash(header.UncleHash) != types.EmptyUncleHash {
		hasUncles = "yes"
	}
	return fmt.Sprintf(`n=%d t=%d hash=%s parent=%s miner=%s uncles=%s txes=%d`,
		header.Number, header.Time, header.Hash[:10], header.ParentHash[:10], header.Coinbase[:10], hasUncles, len(header.Txes))
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("pong"))
}

var statusServerStartedAt time.Time
var statusLatestHead *Header

type ServerStatus struct {
	Uptime       uint64  `json:"uptime"`
	ChainID      uint64  `json:"chain_id"`
	LatestHeader *Header `json:"latest_header"`
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	status := ServerStatus{
		Uptime:       uint64(time.Since(statusServerStartedAt).Round(time.Second).Seconds()),
		ChainID:      chainID.Uint64(),
		LatestHeader: statusLatestHead,
	}
	j, _ := json.MarshalIndent(status, "", "  ")
	w.Header().Set("Content-Type", "application/json")
	w.Write(j)
}

func corsHeaderHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.Header().Set("Access-Control-Allow-Headers", "Origin, Content-Type, X-Auth-Token")
		h.ServeHTTP(w, r)
	})
}

//go:embed orphan-tracker-ui/public/*
var webContent embed.FS

// startHttpServer is copy-pasted from https://stackoverflow.com/a/42533360.
// It allows us to gracefully shutdown the server when the program is interrupted or killed.
func startHttpServer(wg *sync.WaitGroup, db *gorm.DB) *http.Server {
	srv := &http.Server{Addr: httpAddr}

	r := http.NewServeMux()

	subFs, err := fs.Sub(webContent, "orphan-tracker-ui/public")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(subFs))
	r.Handle("/", handlers.LoggingHandler(os.Stderr, fileServer))

	r.Handle("/ping", corsHeaderHandler(handlers.LoggingHandler(os.Stderr, http.HandlerFunc(pingHandler))))
	r.Handle("/status", corsHeaderHandler(handlers.LoggingHandler(os.Stderr, http.HandlerFunc(statusHandler))))
	r.Handle("/api/headers", corsHeaderHandler(handlers.LoggingHandler(os.Stderr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers := []*Header{}
		var res *gorm.DB

		if q := r.URL.Query().Get("raw_sql"); q != "" {
			// Wrap the raw SQL in a transaction so we can rollback afterwards in case anyone feels frisky with
			// mischievous queries.
			tx := db.Begin()
			res = tx.Raw(q).Scan(&headers)
			tx.Rollback()

		} else {

			res = db.Model(&Header{})
			res = res.Order("number DESC")
			res = res.Order("orphan DESC")

			limit := uint64(1000)
			if q := r.URL.Query().Get("limit"); q != "" {
				limit, _ = strconv.ParseUint(q, 10, 64)
			}
			res = res.Limit(int(limit))

			offset := uint64(0)
			if q := r.URL.Query().Get("offset"); q != "" {
				offset, _ = strconv.ParseUint(q, 10, 64)
			}
			res = res.Offset(int(offset))

			if q := r.URL.Query().Get("orphan"); q != "" {
				res = res.Where("orphan = ?", q)
			}

			if q := r.URL.Query().Get("number_min"); q != "" {
				min, _ := strconv.ParseUint(q, 10, 64)
				res = res.Where("number >= ?", min)
			}

			if q := r.URL.Query().Get("number_max"); q != "" {
				max, _ := strconv.ParseUint(q, 10, 64)
				res = res.Where("number <= ?", max)
			}

			if q := r.URL.Query().Get("timestamp_min"); q != "" {
				min, _ := strconv.ParseUint(q, 10, 64)
				res = res.Where("time >= ?", min)
			}

			if q := r.URL.Query().Get("timestamp_max"); q != "" {
				max, _ := strconv.ParseUint(q, 10, 64)
				res = res.Where("time <= ?", max)
			}

			if q := r.URL.Query().Get("include_txes"); q != "false" {
				res = res.Preload("Txes")
			}

			res.Find(&headers)
		}

		if res.Error != nil {
			log.Println(res.Error)
			http.Error(w, res.Error.Error(), http.StatusInternalServerError)
			return
		}

		j, err := json.MarshalIndent(headers, "", "  ")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(j)
	}))))

	r.Handle("/api/txes", corsHeaderHandler(handlers.LoggingHandler(os.Stderr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		txes := []Tx{}
		var res *gorm.DB

		if q := r.URL.Query().Get("raw_sql"); q != "" {
			// Wrap the raw SQL in a transaction so we can rollback afterwards in case anyone feels frisky with
			// mischievous queries.
			tx := db.Begin()
			res = tx.Raw(q).Scan(&txes)
			tx.Rollback()

		} else {
			res = db.Model(Tx{})
			res = res.Order("created_at DESC")

			limit := uint64(1000)
			if q := r.URL.Query().Get("limit"); q != "" {
				limit, _ = strconv.ParseUint(q, 10, 64)
			}
			res = res.Limit(int(limit))

			offset := uint64(0)
			if q := r.URL.Query().Get("offset"); q != "" {
				offset, _ = strconv.ParseUint(q, 10, 64)
			}
			res = res.Offset(int(offset))

			if q := r.URL.Query().Get("include_headers"); q != "false" {
				res = res.Preload("Headers")
			}

			res.Find(&txes)
		}

		if res.Error != nil {
			log.Println(res.Error)
			http.Error(w, res.Error.Error(), http.StatusInternalServerError)
			return
		}

		j, err := json.MarshalIndent(txes, "", "  ")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(j)
	}))))

	srv.Handler = r

	statusServerStartedAt = time.Now()
	go func() {
		defer wg.Done() // let main know we are done cleaning up

		log.Println("Starting HTTP server...", srv.Addr)

		// always returns error. ErrServerClosed on graceful close
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			// unexpected error. port in use?
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	// returning reference so caller can call Shutdown()
	return srv
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}

		// Search config in home directory with name ".go-orphan-tracker" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigName(".go-orphan-tracker")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Println("Using config file:", viper.ConfigFileUsed())
	}
}
