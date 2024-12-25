/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"log"
	"os/exec"

	"github.com/spf13/cobra"
)

// logCmd represents the log command
var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Displays cloud resource file differences before and after the generate command is executed",

	Run: logCommand,
}

func init() {
	rootCmd.AddCommand(logCmd)
}

func logCommand(cmd *cobra.Command, args []string) {

	// Run git config command
	gitConfigCmd := exec.Command("git", "config", "core.quotepath", "false")
	_, err := gitConfigCmd.Output()
	if err != nil {
		log.Printf("error running git config: %v", err)
	}

	// Run git log command
	gitLogCmd := exec.Command("git", "log", "-1", "--pretty=%B")
	gitLogCmdOutput, err := gitLogCmd.Output()
	if err != nil {
		log.Printf("error running git log: %v", err)
	}

	log.Printf("%v\n", string(gitLogCmdOutput))
}
