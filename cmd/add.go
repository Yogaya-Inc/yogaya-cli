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
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2/google"
)

// addCmd represents the add command
var addCmd = &cobra.Command{
	Use:   "add [provider-name] [.yogaya/cloud_accounts.conf-file-path] [provider-credentials-file-path]",
	Short: "Initialize a cloud account with credentials",
	Run:   addCommand,
}

func init() {
	rootCmd.DisableFlagParsing = true
	rootCmd.AddCommand(addCmd)
}

// CloudAccount represents a single cloud account configuration
type CloudAccount struct {
	ID            string      `json:"id"`
	Provider      string      `json:"provider"`
	AddedAt       time.Time   `json:"added_at"`
	LastValidated time.Time   `json:"last_validated"`
	Credentials   interface{} `json:"credentials"`
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
	SubscriptionID string `json:"subscription_id"`
	TenantID       string `json:"tenant_id"`
	Name           string `json:"name"`
	Environment    string `json:"environment"`
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
	data, err := os.ReadFile(cm.configPath)
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
	return os.WriteFile(cm.configPath, data, 0600)
}

// generateAccountID generates a unique ID for an account based on its credentials
func (cm *CredentialManager) generateAccountID(account *CloudAccount) string {
	hash := sha256.New()
	switch account.Provider {
	case "aws":
		hash.Write([]byte(account.Credentials.(*AWSCredentials).AccessKeyID + account.Credentials.(*AWSCredentials).Region))
	case "gcp":
		hash.Write([]byte(account.Credentials.(*GCPCloudCredentials).ProjectID + account.Credentials.(*GCPCloudCredentials).ClientEmail))
	case "azure":
		hash.Write([]byte(account.Credentials.(*AzureCredentials).SubscriptionID + account.Credentials.(*AzureCredentials).TenantID))
	}
	return hex.EncodeToString(hash.Sum(nil))[:12]
}

// isDuplicateAccount checks if an account already exists
func (cm *CredentialManager) isDuplicateAccount(newAccount *CloudAccount) bool {
	newID := cm.generateAccountID(newAccount)
	for _, account := range cm.config.Accounts {
		if account.ID == newID {
			return true
		}
	}
	return false
}

// AddCredentials adds new cloud provider credentials
func (cm *CredentialManager) AddCredentials(provider, credentialsPath string) error {
	credentials, err := cm.readCredentialsFile(provider, credentialsPath)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %v", err)
	}

	newAccount := &CloudAccount{
		Provider:    provider,
		AddedAt:     time.Now(),
		Credentials: credentials,
	}

	if cm.isDuplicateAccount(newAccount) {
		return fmt.Errorf("duplicate account: credentials for this account already exist")
	}

	if err := cm.validateCredentials(provider, credentials); err != nil {
		return fmt.Errorf("credential validation failed: %v", err)
	}

	newAccount.LastValidated = time.Now()
	newAccount.ID = cm.generateAccountID(newAccount)
	cm.config.Accounts = append(cm.config.Accounts, *newAccount)

	return cm.saveConfig()
}

// readCredentialsFile reads and parses the credentials file
func (cm *CredentialManager) readCredentialsFile(provider, path string) (interface{}, error) {
	switch provider {
	case "aws":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return parseAWSCredentials(data)
	case "gcp":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return parseGCPCredentials(data)
	case "azure":
		return getAzureCredentialsFromCLI()
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

// parseAWSCredentials parses AWS credentials from INI format
func parseAWSCredentials(data []byte) (interface{}, error) {
	lines := strings.Split(string(data), "\n")
	creds := &AWSCredentials{}

	var currentProfile string
	var awsAccessKeyId, awsSecretAccessKey, region string

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
				awsAccessKeyId = strings.SplitN(line, "=", 2)[1]
			} else if strings.HasPrefix(line, "aws_secret_access_key") {
				awsSecretAccessKey = strings.SplitN(line, "=", 2)[1]
			} else if strings.HasPrefix(line, "region") {
				region = strings.SplitN(line, "=", 2)[1]
			}
		}
	}

	// return awsCreds, nil
	awsAccessKeyId = strings.TrimSpace(awsAccessKeyId)
	awsSecretAccessKey = strings.TrimSpace(awsSecretAccessKey)
	region = strings.TrimSpace(region)

	creds.AccessKeyID = awsAccessKeyId
	creds.SecretAccessKey = awsSecretAccessKey
	creds.Region = region

	return creds, nil
}

// parseGCPCredentials parses GCP credentials from JSON format
func parseGCPCredentials(data []byte) (interface{}, error) {
	creds := &GCPCloudCredentials{}

	err := json.Unmarshal(data, creds)
	if err != nil {
		log.Fatalf("Unable to decode JSON: %v", err)
	}

	return creds, nil
}

// getAzureCredentialsFromCLI retrieves Azure credentials from Azure CLI
func getAzureCredentialsFromCLI() (*AzureCredentials, error) {
	accountCmd := exec.Command("az", "account", "show")
	output, err := accountCmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get Azure account info. Please ensure you're logged in with 'az login': %v", err)
	}

	var accountInfo struct {
		ID          string `json:"id"`
		TenantID    string `json:"tenantId"`
		Name        string `json:"name"`
		Environment string `json:"environmentName"`
	}

	if err := json.Unmarshal(output, &accountInfo); err != nil {
		return nil, fmt.Errorf("failed to parse Azure CLI output: %v", err)
	}

	return &AzureCredentials{
		SubscriptionID: accountInfo.ID,
		TenantID:       accountInfo.TenantID,
		Name:           accountInfo.Name,
		Environment:    accountInfo.Environment,
	}, nil
}

// validateCredentials checks if the credentials have read-only permissions
func (cm *CredentialManager) validateCredentials(provider string, creds interface{}) error {
	switch provider {
	case "aws":
		awsCreds, ok := creds.(*AWSCredentials)
		if !ok {
			return fmt.Errorf("invalid AWS credentials type")
		}
		return cm.validateAwsCredentials(*awsCreds)
	case "gcp":
		gcpCreds, ok := creds.(*GCPCloudCredentials)
		if !ok {
			return fmt.Errorf("invalid GCP credentials type")
		}
		return cm.validateGcpCredentials(*gcpCreds)
	case "azure":
		azureCreds, ok := creds.(*AzureCredentials)
		if !ok {
			return fmt.Errorf("invalid Azure credentials type")
		}
		return cm.validateAzureCredentials(*azureCreds)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}
}

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

	// List IAM users and check if their credentials work
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
	credJSON, err := json.MarshalIndent(map[string]interface{}{
		"type":           "service_account",
		"project_id":     creds.ProjectID,
		"private_key_id": creds.PrivateKeyID,
		"private_key":    creds.PrivateKey,
		"client_email":   creds.ClientEmail,
		"client_id":      creds.ClientID,
	}, "", "  ")
	if err != nil {
		return err
	}

	credentials, err := google.CredentialsFromJSON(ctx, credJSON, "https://www.googleapis.com/auth/cloud-platform.read-only")
	if err != nil {
		return err
	}

	_, err = credentials.TokenSource.Token()
	return err
}

// validateAzureCredentials validates Azure credentials without simulating policies
func (cm *CredentialManager) validateAzureCredentials(creds AzureCredentials) error {
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure credential: %v", err)
	}

	client, err := armresources.NewResourceGroupsClient(creds.SubscriptionID, credential, nil)
	if err != nil {
		return fmt.Errorf("failed to create Azure resource groups client: %v", err)
	}

	pager := client.NewListPager(nil)
	_, err = pager.NextPage(context.Background())
	if err != nil {
		return fmt.Errorf("failed to validate Azure credentials: %v", err)
	}

	return nil
}

// ListAccounts prints all configured accounts
func (cm *CredentialManager) ListAccounts() {
	fmt.Printf("Configured Cloud Accounts:\n\n")
	for _, account := range cm.config.Accounts {
		fmt.Printf("ID: %s\n", account.ID)
		fmt.Printf("Provider: %s\n", account.Provider)
		fmt.Printf("Added: %s\n", account.AddedAt.Format(time.RFC3339))
		fmt.Printf("Last Validated: %s\n", account.LastValidated.Format(time.RFC3339))
		fmt.Printf("-------------------\n")
	}
}

// addCommand adds a cloud account with the credentials.
func addCommand(cmd *cobra.Command, args []string) {
	if len(args) != 3 {
		fmt.Println("Usage: yogaya add <provider-name> <.yogaya/cloud_accounts.conf-file-path> <provider-credentials-file-path>")
		return
	}

	provider, configPath, credentialsFile := args[0], args[1], args[2]

	cm, err := NewCredentialManager(configPath)
	if err != nil {
		fmt.Printf("Error initializing credential manager: %v\n", err)
		return
	}

	if err := cm.AddCredentials(provider, credentialsFile); err != nil {
		fmt.Printf("Error adding credentials: %v\n", err)
		return
	}

	fmt.Printf("Successfully added %s account\n", provider)
	cm.ListAccounts()
}
