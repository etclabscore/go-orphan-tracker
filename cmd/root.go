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
	"net/http"
	"os"
	"os/signal"
	"sync"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/spf13/cobra"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm/clause"

	"github.com/ethereum/go-ethereum/ethclient"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"

	"gorm.io/gorm"
)

var cfgFile string
var rpcTarget string
var dbPath string

type Head struct {
	gorm.Model

	// Hash is the SAME VALUE as Header.Hash(), but we get to tell gorm that it must be unique.
	Hash string `gorm:"uniqueIndex",json:"hash"`

	// types.Header:
	ParentHash  string `json:"parentHash"`
	UncleHash   string `json:"sha3Uncles"`
	Coinbase    string `json:"miner"`
	Root        string `json:"stateRoot"`
	TxHash      string `json:"transactionsRoot"`
	ReceiptHash string `json:"receiptsRoot"`
	Difficulty  string `json:"difficulty"`
	Number      uint64 `json:"number"`
	GasLimit    uint64 `json:"gasLimit"`
	GasUsed     uint64 `json:"gasUsed"`
	Time        uint64 `json:"timestamp"`
	Extra       []byte `json:"extraData"`
	MixDigest   string `json:"mixHash"`
	Nonce       uint64 `json:"nonce"`
	BaseFee     string `json:"baseFeePerGas"` // BaseFee was added by EIP-1559 and is ignored in legacy headers.

	Orphan bool `gorm:"default:false",json:"orphan"`
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "go-orphan-tracker",
	Short: "A brief description of your application",
	Long: `A longer description that spans multiple lines and likely contains
examples and usage of using your application. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
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

		if err := db.AutoMigrate(&Head{}); err != nil {
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
					log.Println("New side head:", headerStr(header))

					head := appHeader(header, true)

					db.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "hash"}},
						DoUpdates: clause.AssignmentColumns([]string{"orphan"}),
					}).Create(head)

					// Now query and store the block by number to get the canonical block.
					canonHeader, err := client.HeaderByNumber(context.Background(), header.Number)
					if err != nil {
						log.Println(err)
						quitCh <- os.Interrupt
						return
					}

					canonHead := appHeader(canonHeader, false)
					db.Clauses(clause.OnConflict{
						Columns:   []clause.Column{{Name: "hash"}},
						DoUpdates: clause.AssignmentColumns([]string{"orphan"}),
					}).Create(canonHead)

					// Canons
				case err := <-headSub.Err():
					log.Println(err)
					quitCh <- os.Interrupt
					return

				case header := <-headCh:
					log.Println("New head:", headerStr(header))

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

func headerStr(header *types.Header) string {
	return fmt.Sprintf(`n=%d t=%d hash=%s parent=%s miner=%s`,
		header.Number.Uint64(), header.Time, header.Hash().Hex(), header.ParentHash.Hex(), header.Coinbase.Hex())
}

// startHttpServer is copy-pasted from https://stackoverflow.com/a/42533360.
// It allows us to gracefully shutdown the server when the program is interrupted or killed.
func startHttpServer(wg *sync.WaitGroup, db *gorm.DB) *http.Server {
	srv := &http.Server{Addr: ":8080"}

	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("pong"))
	})

	http.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		heads := []*Head{}
		res := db.Find(&heads)
		log.Printf(`Found %d heads, error: %v`, res.RowsAffected, res.Error)

		j, err := json.MarshalIndent(heads, "", "  ")
		if err != nil {
			log.Println(err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(j)
	})
	go func() {
		defer wg.Done() // let main know we are done cleaning up

		// always returns error. ErrServerClosed on graceful close
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			// unexpected error. port in use?
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	// returning reference so caller can call Shutdown()
	return srv
}

// appHeader translates the original header into a our app specific header struct type.
func appHeader(header *types.Header, isOrphan bool) *Head {
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
		Nonce:       header.Nonce.Uint64(),
		BaseFee:     header.BaseFee.String(),
		Orphan:      isOrphan,
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

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
