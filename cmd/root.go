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
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
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

}

// Head is our app representation of a block header.
// We have to reinvent the wheel because we want to play nice with the database,
// and the database doesn't have a model *big.Ints or common.Hash or block.Nonce, etc.
// All *big.Ints are stored as strings in the database unless they are safely converted to uint64s (ie block number).
// All common.Hashes are stored as strings.
type Head struct {

	// These field are taken from gorm.Model, but omitting the ID field. We'll use Hash instead.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`

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
	Txes []Tx `gorm:"many2many:head_txes;foreignKey:Hash;references:Hash" json:"txes"`

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
	BaseFee     string `json:"baseFeePerGas"` // BaseFee was added by EIP-1559 and is ignored in legacy headers.

	// Orphan is a flag indicating whether this header is an orphan.
	Orphan bool `gorm:"default:false" json:"orphan"`

	// UncleBy is the hash of the block/header listing this uncle as an uncle.
	// If empty, it was not recorded as an uncle.
	UncleBy string `json:"uncleBy"`
}

type Tx struct {
	// These field are taken from gorm.Model, but omitting the ID field. We'll use Hash instead.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`

	Heads []*Head `gorm:"many2many:head_txes;foreignKey:Hash;references:Hash" json:"-"`

	Hash     string `json:"hash" gorm:"unique;index;primaryKey"`
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
func appHeader(header *types.Header, isOrphan bool, uncleRecorderHash string) *Head {
	return &Head{
		Hash:        header.Hash().Hex(),
		ParentHash:  header.ParentHash.Hex(),
		UncleHash:   header.UncleHash.Hex(),
		Coinbase:    header.Coinbase.Hex(),
		Root:        header.Root.Hex(),
		TxHash:      header.TxHash.Hex(),
		ReceiptHash: header.ReceiptHash.Hex(),
		Difficulty:  header.Difficulty.String(),
		Number:      header.Number.Uint64(),
		GasLimit:    header.GasLimit,
		GasUsed:     header.GasUsed,
		Time:        header.Time,
		Extra:       header.Extra,
		MixDigest:   header.MixDigest.Hex(),
		Nonce:       fmt.Sprintf("%d", header.Nonce.Uint64()),
		BaseFee:     header.BaseFee.String(),
		Orphan:      isOrphan,
		UncleBy:     uncleRecorderHash,
	}
}

// CreateOrUpdate creates or updates a header, returning any error.
// assignCols should be any of "uncle" or "orphan"; these are the fields which
// are permitted to be updated in case the record already exists.
func (h *Head) CreateOrUpdate(db *gorm.DB, assignCols ...string) error {
	cols := []string{}
	cols = append(cols, assignCols...)
	res := db.
		// Session(&gorm.Session{FullSaveAssociations: true}).
		Clauses(
			clause.OnConflict{
				Columns:   []clause.Column{{Table: "heads", Name: "hash"}},
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
		tx.Heads = []*Head{h}
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

eth_subscribeNewHeads is used to subscribe to new blocks, but is used only for status logging.
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

		if err := db.AutoMigrate(&Head{}, &Tx{}); err != nil {
			log.Println(err)
			os.Exit(1)
		}

		// Set up the subscriptions and channels
		// --------------------------------------------------
		quitCh := make(chan os.Signal, 10)

		interruptCh := make(chan os.Signal, 1)
		signal.Notify(interruptCh, os.Interrupt, os.Kill)

		sideHeadCh := make(chan *types.Header)
		sideSub, err := client.SubscribeNewSideHead(context.Background(), sideHeadCh)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		headCh := make(chan *types.Header)
		headSub, err := client.SubscribeNewHead(context.Background(), headCh)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		// fetchAndAssignTransactionsForHeader will fetch the transactions for a given header and assign them
		// to the struct pointer passed as an argument.
		fetchAndAssignTransactionsForHeader := func(header *Head) error {
			if header.Txes == nil {
				header.Txes = []Tx{}
			}
			bl, err := client.BlockByHash(context.Background(), common.HexToHash(header.Hash))
			if err != nil {
				return err
			}
			for _, tx := range bl.Transactions() {
				tx, err := appTx(tx, bl.BaseFee())
				if err != nil {
					return err
				}
				header.Txes = append(header.Txes, tx)
			}
			return nil
		}

		// fetchAndStoreCanonicalHeader is a convenience function to fetch and store a canonical header.
		// This is used for both side blocks and uncle blocks, because
		// we always want to keep records of corresponding canonical blocks for both orphans and uncles.
		fetchAndStoreCanonicalHeader := func(number *big.Int) error {
			// Now query and store the block by number to get the canonical headers corresponding to
			// this uncle by height.
			canonBlock, err := client.BlockByNumber(context.Background(), number)
			if err != nil {
				return err
			}

			canonHead := appHeader(canonBlock.Header(), false, "")

			if err := fetchAndAssignTransactionsForHeader(canonHead); err != nil {
				return err
			}

			if err := canonHead.CreateOrUpdate(db, "orphan"); err != nil {
				return err
			}
			return nil
		}

		// Run the main loop.
		// --------------------------------------------------
		go func() {
			for {
				select {
				case sig := <-interruptCh:
					log.Println("Received signal:", sig)
					quitCh <- sig
					return

					// Sides
				case err := <-sideSub.Err():
					log.Println(err)
					quitCh <- os.Interrupt
					return

				case header := <-sideHeadCh:

					sideHead := appHeader(header, true, "")
					if err := fetchAndAssignTransactionsForHeader(sideHead); err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						continue
					}

					log.Println("New side head:", headerStr(sideHead))

					if err := sideHead.CreateOrUpdate(db, "orphan"); err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					// Now query and store the block by number to get the canonical block.
					if err := fetchAndStoreCanonicalHeader(header.Number); err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					// Canons
				case err := <-headSub.Err():
					log.Println(err)
					quitCh <- os.Interrupt
					return

				case header := <-headCh:
					log.Println("New head:", headerStr(appHeader(header, false, "")))

					if header.UncleHash == types.EmptyUncleHash {
						continue
					}

					// The new head has uncles.
					// First we have to get the block to get the uncles.
					bl, err := client.BlockByHash(context.Background(), header.Hash())
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					uncles := bl.Uncles()
					for _, uncle := range uncles {

						uncleHead := appHeader(uncle, false, bl.Hash().Hex())
						if err := fetchAndAssignTransactionsForHeader(uncleHead); err != nil {
							log.Println(err)
							quitCh <- os.Interrupt
							continue
						}

						log.Println("New uncle:", headerStr(uncleHead))

						if err := uncleHead.CreateOrUpdate(db, "uncle_by"); err != nil {
							log.Println(err)
							quitCh <- os.Interrupt
							return
						}

						// Now query and store the block by number to get the canonical headers corresponding to
						// this uncle by height.
						if err := fetchAndStoreCanonicalHeader(uncle.Number); err != nil {
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

		// now close the server gracefully ("shutdown")
		// timeout could be given with a proper context
		if err := srv.Shutdown(context.Background()); err != nil {
			panic(err) // failure/timeout shutting down the server gracefully
		}

		// wait for goroutine started in startHttpServer() to stop
		httpServerExitDone.Wait()

		log.Println("Server shutdown complete")

		sideSub.Unsubscribe()
		headSub.Unsubscribe()

		log.Println("Subscriptions closed")

	},
}

func headerStr(header *Head) string {

	j, _ := json.Marshal(header)
	return string(j)

	// hasUncles := "no"
	// if common.HexToHash(header.UncleHash) != types.EmptyUncleHash {
	// 	hasUncles = "yes"
	// }
	// return fmt.Sprintf(`n=%d t=%d hash=%s parent=%s miner=%s uncles=%s txes=%d`,
	// 	header.Number, header.Time, header.Hash, header.ParentHash, header.Coinbase, hasUncles, len(header.Txes))
}

func pingHandler(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte("pong"))
}

// startHttpServer is copy-pasted from https://stackoverflow.com/a/42533360.
// It allows us to gracefully shutdown the server when the program is interrupted or killed.
func startHttpServer(wg *sync.WaitGroup, db *gorm.DB) *http.Server {
	srv := &http.Server{Addr: ":8080"}

	r := http.NewServeMux()

	r.Handle("/ping", handlers.LoggingHandler(os.Stderr, http.HandlerFunc(pingHandler)))
	r.Handle("/api", handlers.LoggingHandler(os.Stderr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		heads := []*Head{}
		res := db.Model(&Head{}).Preload("Txes").Find(&heads)
		if res.Error != nil {
			log.Println(res.Error)
			http.Error(w, res.Error.Error(), http.StatusInternalServerError)
			return
		}

		j, err := json.MarshalIndent(heads, "", "  ")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(j)
	})))

	srv.Handler = r

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
