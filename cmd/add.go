/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"
)

// addCmd represents the add command
var addCmd = &cobra.Command{
	Use:   "add [cloudServiceName] [accountName] [configPath] [credentialFilePath]",
	Short: "Initialize a cloud account with credentials",
	Run:   addCommand,
}

func init() {
	rootCmd.DisableFlagParsing = true
	rootCmd.AddCommand(addCmd)
}

// CloudAccount represents a single cloud account configuration
type CloudAccount struct {
	ID            string           `json:"id"`
	Provider      string           `json:"provider"`
	AccountName   string           `json:"account_name"`
	AddedAt       time.Time        `json:"added_at"`
	LastValidated time.Time        `json:"last_validated"`
	Credentials   CloudCredentials `json:"credentials"`
}

// CloudCredentials holds the credentials for cloud providers
type CloudCredentials struct {
	AWS   AWSCredentials      `json:"aws,omitempty" yaml:"aws,omitempty"`
	GCP   GCPCloudCredentials `json:"gcp,omitempty" yaml:"gcp,omitempty"`
	Azure AzureCredentials    `json:"azure,omitempty" yaml:"azure,omitempty"`
}

// AWSCredentials represents AWS-specific credentials
type AWSCredentials struct {
	AccessKeyID     string `json:"access_key_id" yaml:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key" yaml:"secret_access_key"`
	Region          string `json:"region" yaml:"region"`
}

// GCPCloudCredentials represents GCP-specific credentials
type GCPCloudCredentials struct {
	ProjectID    string `json:"project_id" yaml:"project_id"`
	PrivateKeyID string `json:"private_key_id" yaml:"private_key_id"`
	PrivateKey   string `json:"private_key" yaml:"private_key"`
	ClientEmail  string `json:"client_email" yaml:"client_email"`
	ClientID     string `json:"client_id" yaml:"client_id"`
}

// AzureCredentials represents Azure-specific credentials
type AzureCredentials struct {
	SubscriptionID string `json:"subscription_id" yaml:"subscription_id"`
	TenantID       string `json:"tenant_id" yaml:"tenant_id"`
	ClientID       string `json:"client_id" yaml:"client_id"`
	ClientSecret   string `json:"client_secret" yaml:"client_secret"`
}

// CloudAccountsConfig represents the structure of cloud_accounts.conf
type CloudAccountsConfig struct {
	Accounts []CloudAccount `json:"accounts"`
}

// CredentialManager handles cloud provider credentials
type CredentialManager struct {
	configPath string
	config     CloudAccountsConfig
}

// NewCredentialManager creates a new credential manager instance
func NewCredentialManager(configPath string) (*CredentialManager, error) {
	cm := &CredentialManager{
		configPath: configPath,
		config:     CloudAccountsConfig{Accounts: []CloudAccount{}},
	}

	if err := cm.loadConfig(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return cm, nil
}

// loadConfig loads the existing configuration from cloud_accounts.conf
func (cm *CredentialManager) loadConfig() error {
	data, err := os.ReadFile(filepath.Join(cm.configPath, "cloud_accounts.conf"))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &cm.config)
}

// saveConfig saves the current configuration to cloud_accounts.conf
func (cm *CredentialManager) saveConfig() error {
	data, err := json.MarshalIndent(cm.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cm.configPath, "cloud_accounts.conf"), data, 0600)
}

// generateAccountID generates a unique ID for an account based on its credentials
func (cm *CredentialManager) generateAccountID(account CloudAccount) string {
	hash := sha256.New()
	switch account.Provider {
	case "aws":
		hash.Write([]byte(account.Credentials.AWS.AccessKeyID + account.Credentials.AWS.Region))
	case "gcp":
		hash.Write([]byte(account.Credentials.GCP.ProjectID + account.Credentials.GCP.ClientEmail))
	case "azure":
		hash.Write([]byte(account.Credentials.Azure.SubscriptionID + account.Credentials.Azure.ClientID))
	}
	return hex.EncodeToString(hash.Sum(nil))[:12]
}

// isDuplicateAccount checks if an account already exists
func (cm *CredentialManager) isDuplicateAccount(newAccount CloudAccount) bool {
	newID := cm.generateAccountID(newAccount)
	for _, account := range cm.config.Accounts {
		if account.ID == newID {
			return true
		}
	}
	return false
}

// AddCredentials adds new cloud provider credentials
func (cm *CredentialManager) AddCredentials(provider, accountName, credentialsPath string) error {
	credentials, err := cm.readCredentialsFile(provider, credentialsPath)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %v", err)
	}

	newAccount := CloudAccount{
		Provider:    provider,
		AccountName: accountName,
		AddedAt:     time.Now(),
		Credentials: *credentials,
	}
	if cm.isDuplicateAccount(newAccount) {
		return fmt.Errorf("duplicate account: credentials for this account already exist")
	}

	if err := cm.validateCredentials(provider, credentials); err != nil {
		return fmt.Errorf("credential validation failed: %v", err)
	}

	newAccount.LastValidated = time.Now()
	newAccount.ID = cm.generateAccountID(newAccount)
	cm.config.Accounts = append(cm.config.Accounts, newAccount)

	return cm.saveConfig()
}

// readCredentialsFile reads and parses the credentials file
func (cm *CredentialManager) readCredentialsFile(provider, path string) (*CloudCredentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	switch provider {
	case "aws":
		return parseAWSCredentials(data)
	case "gcp":
		return parseGCPAndAzureCredentials(data, "gcp")
	case "azure":
		return parseGCPAndAzureCredentials(data, "azure")
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

// parseAWSCredentials parses AWS credentials from INI format
func parseAWSCredentials(data []byte) (*CloudCredentials, error) {
	lines := strings.Split(string(data), "\n")
	creds := &CloudCredentials{
		AWS: AWSCredentials{},
	}

	var currentProfile string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentProfile = line[1 : len(line)-1]
			continue
		}

		if currentProfile == "default" {
			if strings.HasPrefix(line, "aws_access_key_id") {
				creds.AWS.AccessKeyID = strings.SplitN(line, "=", 2)[1]
			} else if strings.HasPrefix(line, "aws_secret_access_key") {
				creds.AWS.SecretAccessKey = strings.SplitN(line, "=", 2)[1]
			} else if strings.HasPrefix(line, "region") {
				creds.AWS.Region = strings.SplitN(line, "=", 2)[1]
			}
		}
	}

	// Trim spaces
	creds.AWS.AccessKeyID = strings.TrimSpace(creds.AWS.AccessKeyID)
	creds.AWS.SecretAccessKey = strings.TrimSpace(creds.AWS.SecretAccessKey)
	creds.AWS.Region = strings.TrimSpace(creds.AWS.Region)

	return creds, nil
}

// parseGCPAndAzureCredentials parses GCP and Azure credentials from INI format
func parseGCPAndAzureCredentials(data []byte, provider string) (*CloudCredentials, error) {
	lines := strings.Split(string(data), "\n")
	creds := &CloudCredentials{}

	for _, line := range lines {
		// line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue // Skip empty lines and comments
		}

		prefix := strings.Trim(strings.Split(strings.TrimSpace(line), ":")[0], "\"")
		// value := strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
		// value = strings.TrimPrefix(value, " ")
		if provider == "gcp" {
			if strings.HasPrefix(prefix, "project_id") {
				creds.GCP.ProjectID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(prefix, "private_key_id") {
				creds.GCP.PrivateKeyID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(prefix, "private_key") {
				creds.GCP.PrivateKey = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(prefix, "client_email") {
				creds.GCP.ClientEmail = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(prefix, "client_id") {
				creds.GCP.ClientID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			}
		} else if provider == "azure" {
			if strings.HasPrefix(line, "subscription_id") {
				creds.Azure.SubscriptionID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(line, "tenant_id") {
				creds.Azure.TenantID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(line, "client_id") {
				creds.Azure.ClientID = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			} else if strings.HasPrefix(line, "client_secret") {
				creds.Azure.ClientSecret = strings.TrimPrefix(strings.Trim(strings.SplitN(line, ":", 2)[1], "\""), " ")
			}
		}
	}

	// Trim spaces
	// if provider == "gcp" {
	// 	creds.GCP.ProjectID = strings.TrimSpace(creds.GCP.ProjectID)
	// 	creds.GCP.PrivateKeyID = strings.TrimSpace(creds.GCP.PrivateKeyID)
	// 	creds.GCP.PrivateKey = strings.TrimSpace(creds.GCP.PrivateKey)
	// 	creds.GCP.ClientEmail = strings.TrimSpace(creds.GCP.ClientEmail)
	// 	creds.GCP.ClientID = strings.TrimSpace(creds.GCP.ClientID)
	// } else if provider == "azure" {
	// 	creds.Azure.SubscriptionID = strings.TrimSpace(creds.Azure.SubscriptionID)
	// 	creds.Azure.TenantID = strings.TrimSpace(creds.Azure.TenantID)
	// 	creds.Azure.ClientID = strings.TrimSpace(creds.Azure.ClientID)
	// 	creds.Azure.ClientSecret = strings.TrimSpace(creds.Azure.ClientSecret)
	// }

	return creds, nil
}

// validateCredentials checks if the credentials have read-only permissions
func (cm *CredentialManager) validateCredentials(provider string, creds *CloudCredentials) error {
	switch provider {
	case "aws":
		return cm.validateAwsCredentials(creds.AWS)
	case "gcp":
		return cm.validateGcpCredentials(creds.GCP)
	case "azure":
		return cm.validateAzureCredentials(creds.Azure)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
}

// validateAwsCredentials validates AWS credentials
// validateAwsCredentials validates AWS credentials without simulating policies
func (cm *CredentialManager) validateAwsCredentials(creds AWSCredentials) error {
	ctx := context.Background()

	// Load the AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(creds.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			creds.AccessKeyID,
			creds.SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return fmt.Errorf("failed to load AWS configuration: %v", err)
	}

	// Create an IAM client
	iamClient := iam.NewFromConfig(cfg)

	// You might still want to check if the credentials work by calling a harmless API
	// For example, list IAM users (if the user has permission)
	input := &iam.ListUsersInput{}

	// This call will not throw an error if the credentials are valid and have the right permissions
	_, err = iamClient.ListUsers(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to validate AWS credentials: %v", err)
	}

	return nil
}

// validateGcpCredentials validates GCP credentials
func (cm *CredentialManager) validateGcpCredentials(creds GCPCloudCredentials) error {
	ctx := context.Background()
	credJSON, err := json.Marshal(map[string]interface{}{
		"type":           "service_account",
		"project_id":     creds.ProjectID,
		"private_key_id": creds.PrivateKeyID,
		"private_key":    creds.PrivateKey,
		"client_email":   creds.ClientEmail,
		"client_id":      creds.ClientID,
	})
	if err != nil {
		return err
	}
	// transedCredJSON, err := json.MarshalIndent(credJSON, "", "  ")
	// if err != nil {
	// 	return err
	// }

	fmt.Printf("creds.PrivateKey:%v\n\n", creds.PrivateKey)
	fmt.Printf("%v\n\n", string(credJSON))

	credentials, err := google.CredentialsFromJSON(ctx, credJSON, "https://www.googleapis.com/auth/cloud-platform.read-only")
	if err != nil {
		return err
	}

	_, err = credentials.TokenSource.Token()
	return err
}

// validateAzureCredentials validates Azure credentials
func (cm *CredentialManager) validateAzureCredentials(creds AzureCredentials) error {
	cred, err := azidentity.NewClientSecretCredential(creds.TenantID, creds.ClientID, creds.ClientSecret, nil)
	if err != nil {
		return err
	}

	client, err := armsubscriptions.NewClient(cred, nil)
	if err != nil {
		return err
	}

	ctx := context.Background()
	_, err = client.Get(ctx, creds.SubscriptionID, nil)
	return err
}

// ListAccounts prints all configured accounts
func (cm *CredentialManager) ListAccounts() {
	fmt.Printf("Configured Cloud Accounts:\n\n")
	for _, account := range cm.config.Accounts {
		fmt.Printf("ID: %s\n", account.ID)
		fmt.Printf("Provider: %s\n", account.Provider)
		fmt.Printf("Account Name: %s\n", account.AccountName)
		fmt.Printf("Added: %s\n", account.AddedAt.Format(time.RFC3339))
		fmt.Printf("Last Validated: %s\n", account.LastValidated.Format(time.RFC3339))
		fmt.Printf("-------------------\n")
	}
}

// addCommand adds a cloud account with the credentials.
func addCommand(cmd *cobra.Command, args []string) {
	if len(args) != 4 {
		fmt.Println("Usage: example add <provider> <account-name> <config-path> <credentials-file>")
		return
	}

	provider, accountName, configPath, credentialsFile := args[0], args[1], args[2], args[3]

	cm, err := NewCredentialManager(filepath.Join(configPath, ".yogaya"))
	if err != nil {
		fmt.Printf("Error initializing credential manager: %v\n", err)
		return
	}

	if err := cm.AddCredentials(provider, accountName, credentialsFile); err != nil {
		fmt.Printf("Error adding credentials: %v\n", err)
		return
	}

	fmt.Printf("Successfully added %s account: %s\n", provider, accountName)
	cm.ListAccounts()
}
