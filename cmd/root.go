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
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/spf13/cobra"

	"github.com/ethereum/go-ethereum/ethclient"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

var cfgFile string
var rpcTarget string

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

		quitCh := make(chan os.Signal, 1)

		sideHeadCh := make(chan *types.Header)
		sideSub, err := client.SubscribeNewSideHead(context.Background(), sideHeadCh)
		if err != nil {
			log.Println(err)
			os.Exit(1)
		}

		headCh := make(chan *types.Header)
		headSub, err := client.SubscribeNewHead(context.Background(), headCh)

		interruptCh := make(chan os.Signal, 1)
		signal.Notify(interruptCh, os.Interrupt, os.Kill)

		go func() {
			for {
				select {
				case sig := <-interruptCh:
					log.Println("Received signal", sig)
					sideSub.Unsubscribe()
					quitCh <- sig

					// Sides
				case err := <-sideSub.Err():
					log.Println(err)
					quitCh <- os.Interrupt

				case header := <-sideHeadCh:
					log.Println("New side head:", header.Number.Uint64())

					// Canons
				case err := <-headSub.Err():
					log.Println(err)
					quitCh <- os.Interrupt

				case header := <-headCh:
					log.Println("New head:", header.Number.Uint64())
				}
			}
		}()

		<-quitCh
	},
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
