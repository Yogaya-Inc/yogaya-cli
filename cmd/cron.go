/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"yogaya-cli/internal/daemon"

	"github.com/spf13/cobra"
)

var timeArg string

// cronCmd represents the cron command
var cronCmd = &cobra.Command{
	Use:   "cron",
	Short: "Run generate with cron scheduling",
	Long: `Run generate with cron scheduling. Time should be specified in HH:MM:SS format.
If no time is specified, it defaults to 00:00:00 UTC.

Examples:
  taskrunner cron             # Run daily at 00:00:00 UTC
  taskrunner cron 10:00:00    # Run daily at 10:00:00 UTC
  taskrunner cron status      # Check scheduled execution status
  taskrunner cron stop        # Stop scheduled execution`,
	Run: cronCommand,
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the scheduled execution status",
	Run: func(cmd *cobra.Command, args []string) {
		if err := daemon.CheckStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the scheduled execution",
	Run: func(cmd *cobra.Command, args []string) {
		if err := daemon.StopDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(cronCmd)
	cronCmd.AddCommand(statusCmd, stopCmd)
}

func cronCommand(cmd *cobra.Command, args []string) {
	// Get time from args
	timeArg := ""
	if len(args) > 0 {
		timeArg = args[0]
	}

	// Initialize and start the daemon with generateExec
	d, err := daemon.NewDaemon(timeArg, generateExec)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := d.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
