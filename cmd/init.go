/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/spf13/cobra"
)

// initCmd represents the init command
var initCmd = &cobra.Command{
	Use:   "init",
	Short: "init",
	Long: `A longer description that spans multiple lines and likely contains yogayas
and usage of using your command. For yogaya:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Start of initialization process")
		initCommand(args[0])
	},
}

func init() {
	rootCmd.AddCommand(initCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// initCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// initCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initCommand initializes the repository and configuration files
func initCommand(customPath string) {
	var yogayaDir string
	if customPath != "" {
		yogayaDir = fmt.Sprintf("%s/.yogaya", customPath)
	} else {
		homeDir, _ := os.UserHomeDir()
		yogayaDir = fmt.Sprintf("%s/.yogaya", homeDir)
	}

	// Create .yogaya directory
	os.MkdirAll(yogayaDir, os.ModePerm)

	// Create tenant.conf
	tenantConf := fmt.Sprintf("%s/tenant.conf", yogayaDir)
	time := time.Now()
	// tenantKey変数にtimeをハッシュ化した値を代入（ハッシュ化については要精査）
	tenantKey := fmt.Sprintf("%x", time)
	_ = os.WriteFile(tenantConf, []byte(fmt.Sprintf("tenant_key=%s", tenantKey)), 0644)

	// Create cloud_accounts.conf
	cloudConf := fmt.Sprintf("%s/cloud_accounts.conf", yogayaDir)
	_ = os.WriteFile(cloudConf, []byte(""), 0644)

	// Initialize Git repository
	exec.Command("git", "init", yogayaDir).Run()

	fmt.Println("Initialized configuration in", yogayaDir)
}
