/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a yogaya Application",
	// Long:  `aaaaaaaaaaaa`,
	Run: initCommand,
}

func init() {
	rootCmd.DisableFlagParsing = true
	rootCmd.AddCommand(initCmd)
}

// initCommand initializes the repository and configuration files
func initCommand(cmd *cobra.Command, args []string) {
	fmt.Println("Start of initialization process")

	yogayaProjectDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	yogayaDir := fmt.Sprintf("%s/.yogaya", yogayaProjectDir)

	// Create .yogaya directory
	os.MkdirAll(yogayaDir, os.ModePerm)

	// Create tenant.conf
	tenantConf := fmt.Sprintf("%s/tenant.conf", yogayaDir)
	time := time.Now()
	// TBD:Details of tenant key will be decided later.
	tenantKey := hashingTime(time)
	_ = os.WriteFile(tenantConf, []byte(fmt.Sprintf("tenant_key=%s", tenantKey)), 0644)

	// Create cloud_accounts.conf
	cloudConf := fmt.Sprintf("%s/cloud_accounts.conf", yogayaDir)
	_ = os.WriteFile(cloudConf, []byte("{}"), 0644)

	// Initialize Git repository
	gitInitCmd := exec.Command("git", "init", yogayaProjectDir)

	// readlinkCmd := exec.Command("readlink", "-f", yogayaProjectDir)
	gitInitCmdErr := gitInitCmd.Run()
	if gitInitCmdErr != nil {
		fmt.Printf("error: %v\n", gitInitCmdErr)
		fmt.Printf("If you do not have Git installed locally, please install it and re-run this command.\n")
		return
	}

	// Create .gitignore
	createGitIgnoreFile(yogayaProjectDir)

	gitAddCommitCmd := exec.Command("git", "add", ".")
	gitAddCommitCmdErr := gitAddCommitCmd.Run()
	if gitAddCommitCmdErr != nil {
		fmt.Printf("error: %v\n", gitAddCommitCmdErr)
		return
	}

	gitInitialCommitCmd := exec.Command("git", "commit", "-m", "initial commit")
	gitInitialCommitCmdErr := gitInitialCommitCmd.Run()
	if gitInitialCommitCmdErr != nil {
		fmt.Printf("error: %v\n", gitInitialCommitCmdErr)
		return
	}

	fmt.Println("Completed initialization process!")
	fmt.Println("Initialized configuration in", yogayaProjectDir)
}

// HashingTime takes a time.Time value and returns its SHA-256 hash as a hexadecimal string.
func hashingTime(t time.Time) string {
	// Convert time.Time to string in RFC 3339 format
	timeString := t.Format(time.RFC3339)

	// Create a new SHA-256 hash
	hash := sha256.New()
	// Write the byte representation of the string to the hash
	hash.Write([]byte(timeString))
	// Get the final hash value
	hashBytes := hash.Sum(nil)

	// Return the hash value as a hexadecimal string
	return hex.EncodeToString(hashBytes)
}

func createGitIgnoreFile(dir string) error {

	gitIgnoreText := `
# Generated init command directory
.yogaya/*

# Mac OS
.DS_Store
`

	err := os.WriteFile(fmt.Sprintf("%s/.gitignore", dir), []byte(gitIgnoreText), 0644)
	if err != nil {
		return fmt.Errorf("error writing .gitignore: %v", err)
	}

	return nil
}
