/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// addCmd represents the add command
var addCmd = &cobra.Command{
	Use:   "add [cloudServiceName] [.yogayaDirPath] [credentialFilePath]",
	Short: "Initialize a yogaya Application",
	// Long:  `aaaaaaaaaaaa`,
	Run: addCommand,
}

func init() {
	rootCmd.DisableFlagParsing = true
	rootCmd.AddCommand(addCmd)
}

// CloudAccount represents the structure of a cloud account entry.
type CloudAccount struct {
	CloudService string `json:"cloudService"`
	User         string `json:"user"`
	Credentials  string `json:"credentials"` // Store the credentials directly
}

// addCommand adds a cloud account with the credentials.
func addCommand(cmd *cobra.Command, args []string) {
	cloudService := args[0]
	yogayaDirPath := args[1]
	credentialFilePath := args[2]

	// Get the path to cloud_accounts.conf
	cloudConf := fmt.Sprintf("%s/.yogaya/cloud_accounts.conf", yogayaDirPath)

	// Validate the cloud service name
	validServices := map[string]bool{
		"aws":   true,
		"gcp":   true,
		"azure": true,
	}

	if _, exists := validServices[cloudService]; !exists {
		fmt.Println("Invalid cloud service name. Available services: aws, gcp, azure")
		return
	}

	// Check if the credential file exists
	if _, err := os.Stat(credentialFilePath); os.IsNotExist(err) {
		fmt.Printf("The credential file does not exist: %s\n", credentialFilePath)
		return
	}

	// Determine the user and credentials based on the service
	user, credentials := extractUserAndCredentialsFromCredentialFile(cloudService, credentialFilePath)
	if user == "" || credentials == "" {
		fmt.Println("Unable to determine the user or credentials from the credential file.")
		return
	}

	// Load existing accounts
	var accounts []CloudAccount
	if _, err := os.Stat(cloudConf); err == nil {
		file, _ := os.ReadFile(cloudConf)
		json.Unmarshal(file, &accounts)
	}

	// Check for duplicates
	for _, account := range accounts {
		if account.CloudService == cloudService && account.User == user {
			fmt.Println("This user already exists for the specified cloud service.")
			return
		}
	}

	// Create new account entry
	newAccount := CloudAccount{
		CloudService: cloudService,
		User:         user,
		Credentials:  credentials,
	}
	accounts = append(accounts, newAccount)

	// Save updated accounts back to cloud_accounts.conf
	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling JSON: %s\n", err)
		return
	}

	err = os.WriteFile(cloudConf, data, 0644)
	if err != nil {
		fmt.Printf("Unable to write to cloud_accounts.conf: %s\n", err)
		return
	}

	fmt.Printf("Cloud account for user '%s' under cloud service '%s' has been added.\n", user, cloudService)
}

// extractUserAndCredentialsFromCredentialFile determines the user and credentials from the credential file based on the cloud service.
func extractUserAndCredentialsFromCredentialFile(service string, credentialFilePath string) (string, string) {
	switch service {
	case "aws":
		return extractAWSUserAndCredentials(credentialFilePath)
	case "gcp":
		return extractGCPUserAndCredentials(credentialFilePath)
	case "azure":
		return extractAzureUserAndCredentials(credentialFilePath)
	default:
		return "", ""
	}
}

// extractAWSUserAndCredentials extracts the AWS user name and credentials from the credential file.
func extractAWSUserAndCredentials(credentialFilePath string) (string, string) {
	// Read the AWS credentials file
	data, err := os.ReadFile(credentialFilePath)
	if err != nil {
		return "", ""
	}

	// Parse the AWS credentials (assuming the default format)
	lines := strings.Split(string(data), "\n")
	var user, accessKey, secretKey string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			user = strings.Trim(line, "[]")
		} else if strings.HasPrefix(line, "aws_access_key_id=") {
			accessKey = strings.Split(line, "=")[1]
		} else if strings.HasPrefix(line, "aws_access_key_id =") {
			accessKey = strings.Split(line, " = ")[1]
		} else if strings.HasPrefix(line, "aws_secret_access_key=") {
			secretKey = strings.Split(line, "=")[1]
		} else if strings.HasPrefix(line, "aws_secret_access_key =") {
			secretKey = strings.Split(line, " = ")[1]
		}
	}

	if user != "" && accessKey != "" && secretKey != "" {
		return user, fmt.Sprintf("AccessKey=%s;SecretKey=%s", accessKey, secretKey)
	}
	return "", ""
}

// extractGCPUserAndCredentials extracts the GCP user name and credentials from the credential file.
func extractGCPUserAndCredentials(credentialFilePath string) (string, string) {
	// Read the GCP credentials JSON file
	data, err := os.ReadFile(credentialFilePath)
	if err != nil {
		return "", ""
	}

	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err == nil {
		if email, ok := jsonData["client_email"].(string); ok {
			credentials := jsonData["private_key"].(string)
			return email, credentials
		}
	}
	return "", ""
}

// extractAzureUserAndCredentials extracts the Azure user name and credentials from the credential file.
func extractAzureUserAndCredentials(credentialFilePath string) (string, string) {
	// Read the Azure credentials file
	data, err := os.ReadFile(credentialFilePath)
	if err != nil {
		return "", ""
	}

	// Assume Azure credentials are in a specific format (customize as needed)
	lines := strings.Split(string(data), "\n")
	var user, clientSecret string
	for _, line := range lines {
		if strings.HasPrefix(line, "clientId:") {
			user = strings.TrimSpace(strings.Split(line, ":")[1])
		} else if strings.HasPrefix(line, "clientSecret:") {
			clientSecret = strings.TrimSpace(strings.Split(line, ":")[1])
		}
	}
	if user != "" && clientSecret != "" {
		return user, clientSecret
	}
	return "", ""
}
