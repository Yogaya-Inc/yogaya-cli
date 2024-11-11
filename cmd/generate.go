// cmd/generate.go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	awsprovider "github.com/GoogleCloudPlatform/terraformer/providers/aws"
	gcpprovider "github.com/GoogleCloudPlatform/terraformer/providers/gcp"
	"github.com/GoogleCloudPlatform/terraformer/terraformutils"
	"github.com/spf13/cobra"
)

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate [.yogaya/cloud_accounts.conf-file-path]",
	Short: "Generate Terraform code from cloud resources",
	Run:   generateCommand,
}

func init() {
	rootCmd.DisableFlagParsing = true
	rootCmd.AddCommand(generateCmd)
}

// runGenerate handles the main generation process
func generateCommand(cmd *cobra.Command, args []string) {
	if len(args) != 1 {
		fmt.Println("Usage: yogaya generate <.yogaya/cloud_accounts.conf-file-path>")
		return
	}

	// Initialize credential manager
	credManager := &CredentialManager{
		configPath: args[0],
	}

	// Read and parse the credentials file
	if err := credManager.loadConfig(); err != nil {
		fmt.Printf("failed to load config: %v", err)
		return
	}

	// Process each account
	for _, account := range credManager.config.Accounts {
		fmt.Printf("Starting generate Terraform code for %v in %v\n", account.ID, account.Provider)
		if err := processAccount(account); err != nil {
			fmt.Printf("Error processing account %s: %v\n", account.ID, err)
			fmt.Printf("-------------------\n")
			continue
		}
		fmt.Printf("Generate Terraform code for %v in %v Completed!\n", account.ID, account.Provider)
		fmt.Printf("-------------------\n")
	}
}

// processAccount handles the account-specific processing logic
func processAccount(account CloudAccount) error {
	switch account.Provider {
	case "aws":
		return processAWSAccount(account)
	case "gcp":
		return processGCPAccount(account)
	default:
		return fmt.Errorf("unsupported provider: %s", account.Provider)
	}
}

// generateTerraformCode generates Terraform configuration for the specified provider
func generateTerraformCode(provider terraformutils.ProviderGenerator, region, outputDir string) error {
	supportedServices := provider.GetSupportedService()

	// Create a map to hold resources by region
	resourcesByRegion := make(map[string][]terraformutils.Resource)
	existingResourceFlag := false

	// Initialize each service and retrieve resources
	for serviceName := range supportedServices {
		err := provider.InitService(serviceName, true)
		if err != nil {
			fmt.Printf("Error initializing service %s: %v\n", serviceName, err)
			continue
		}

		// Retrieve resources for the service
		service := provider.GetService()
		if service == nil {
			fmt.Printf("Service is nil for: %s\n", serviceName)
			continue
		}

		// fmt.Printf("Retrieving resources for service: %s\n", serviceName)
		resources := service.GetResources()

		// Check if resources were retrieved successfully
		if len(resources) > 0 {
			fmt.Printf("Found %d resources for service: %s\n", len(resources), serviceName)
			existingResourceFlag = true

			// Debug output of resource detail
			for _, r := range resources {
				fmt.Printf("Resource Type: %s, Name: %s\n", r.InstanceInfo.Type, r.ResourceName)
			}
		}
		//  else {
		// 	fmt.Printf("No resources found for service: %s\n", serviceName)
		// }

		// Organize resources by region
		resourcesByRegion[region] = append(resourcesByRegion[region], resources...)

	}

	// Remove empty directories
	if !existingResourceFlag {
		// err := removeDirRecursive(outputDir)
		err := os.RemoveAll(outputDir)
		if err != nil {
			fmt.Printf("Failed to remove directory: %v\n", err)
		}
		return nil
	}

	// Write resources to Terraform config files by region
	for region, resources := range resourcesByRegion {
		if err := writeTerraformConfig(resources, outputDir, region); err != nil {
			return fmt.Errorf("failed to write Terraform config for region %s: %w", region, err)
		}
	}

	return nil
}

// writeTerraformConfig generates a Terraform configuration file from the resources for a specific region
func writeTerraformConfig(resources []terraformutils.Resource, outputDir string, region string) error {
	// Create the output directory
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// String builder for the Terraform configuration
	var terraformConfig strings.Builder

	// Loop through resources and generate the configuration
	for _, resource := range resources {
		// Get the resource type and name
		terraformConfig.WriteString(fmt.Sprintf("resource \"%s\" \"%s\" {\n", resource.InstanceInfo.Type, resource.ResourceName))

		// Add each resource's attributes to the configuration
		for key, value := range resource.Item {
			terraformConfig.WriteString(fmt.Sprintf("  %s = %v\n", key, formatValue(value)))
		}

		terraformConfig.WriteString("}\n\n")
	}

	// Set the output file name for the region's resources
	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s_resources.tf", region))
	if err := os.WriteFile(outputFile, []byte(terraformConfig.String()), 0644); err != nil {
		return fmt.Errorf("failed to write to Terraform file: %v", err)
	}

	return nil
}

// formatValue formats the resource values for the Terraform configuration
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", val) // Enclose strings in quotes
	case []interface{}:
		if len(val) == 0 {
			return "[]"
		}
		result := "[\n"
		for _, item := range val {
			result += fmt.Sprintf("    %v,\n", formatValue(item))
		}
		result += "  ]"
		return result
	case map[string]interface{}:
		if len(val) == 0 {
			return "{}"
		}
		result := "{\n"
		for k, v := range val {
			result += fmt.Sprintf("    %s = %v\n", k, formatValue(v))
		}
		result += "  }"
		return result
	default:
		return fmt.Sprintf("%v", val)
	}
}

// processAWSAccount handles AWS-specific account processing
func processAWSAccount(account CloudAccount) error {
	// Convert credentials from interface{} to concrete type
	creds, ok := account.Credentials.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid AWS credentials format")
	}

	awsCreds := AWSCredentials{
		AccessKeyID:     creds["access_key_id"].(string),
		SecretAccessKey: creds["secret_access_key"].(string),
	}

	// Set AWS credentials as environment variables
	os.Setenv("AWS_ACCESS_KEY_ID", awsCreds.AccessKeyID)
	os.Setenv("AWS_SECRET_ACCESS_KEY", awsCreds.SecretAccessKey)

	// Get list of AWS regions
	regions := getAWSRegions()

	// Generate Terraform code for each region
	for _, region := range regions {
		// Create output directory structure
		outputDir := filepath.Join("generated", fmt.Sprintf("aws-%s", account.ID), region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %v", err)
		}

		// Generate provider configuration
		if err := generateProviderConfig(account, outputDir); err != nil {
			return fmt.Errorf("failed to generate provider config: %v", err)
		}

		// Run terraform init
		cmd := exec.Command("terraform", "init")
		cmd.Dir = outputDir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run terraform init: %v", err)
		}

		// Initialize AWS provider with region
		provider := &awsprovider.AWSProvider{}
		os.Setenv("AWS_DEFAULT_REGION", region)

		err := provider.Init([]string{
			region,    // args[0]: リージョン
			"default", // args[1]: プロファイル名
		})
		if err != nil {
			return fmt.Errorf("failed to initialize AWS provider: %v", err)
		}

		// Generate Terraform configuration
		if err := generateTerraformCode(provider, region, outputDir); err != nil {
			fmt.Printf("Error generating Terraform code for region %s: %v\n", region, err)
			continue
		}
	}

	return nil
}

// processGCPAccount handles GCP-specific account processing
func processGCPAccount(account CloudAccount) error {
	// Convert credentials from interface{} to concrete type
	creds, ok := account.Credentials.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid GCP credentials format")
	}

	gcpCreds := GCPCloudCredentials{
		ProjectID:    creds["project_id"].(string),
		PrivateKeyID: creds["private_key_id"].(string),
		PrivateKey:   creds["private_key"].(string),
		ClientEmail:  creds["client_email"].(string),
		ClientID:     creds["client_id"].(string),
	}

	// Create temporary file for GCP credentials
	tempFile, err := createTempGCPCredentials(gcpCreds)
	if err != nil {
		return fmt.Errorf("failed to create temporary GCP credentials: %v", err)
	}

	// Set GCP authentication environment variable
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tempFile)
	os.Setenv("GOOGLE_CLOUD_PROJECT", gcpCreds.ProjectID)

	// Get list of GCP regions
	regions := getGCPRegions()

	// Generate Terraform code for each region
	for _, region := range regions {
		// Create output directory structure
		outputDir := filepath.Join("generated", fmt.Sprintf("gcp-%s", account.ID), region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %v", err)
		}

		// Generate provider configuration
		if err := generateProviderConfig(account, outputDir); err != nil {
			return fmt.Errorf("failed to generate provider config: %v", err)
		}

		// Run terraform init
		cmd := exec.Command("terraform", "init")
		cmd.Dir = outputDir
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to run terraform init: %v", err)
		}

		// Initialize GCP provider with region
		provider := &gcpprovider.GCPProvider{}
		os.Setenv("GOOGLE_CLOUD_REGION", region)

		err := provider.Init([]string{
			region,             // args[0]: region
			gcpCreds.ProjectID, // args[1]: projectName(ProjectID)
			"google",           // args[2]: providerType
		})
		if err != nil {
			return fmt.Errorf("failed to initialize GCP provider: %v", err)
		}

		// Generate Terraform configuration
		if err := generateTerraformCode(provider, region, outputDir); err != nil {
			fmt.Printf("Error generating Terraform code for region %s: %v\n", region, err)
			continue
		}
	}

	return nil
}

// Helper functions for region handling
func getAWSRegions() []string {
	return []string{
		"us-east-1",      // N. Virginia
		"us-east-2",      // Ohio
		"us-west-1",      // N. California
		"us-west-2",      // Oregon
		"af-south-1",     // Cape Town
		"ap-east-1",      // Hong Kong
		"ap-northeast-1", // Tokyo
		"ap-northeast-2", // Seoul
		"ap-northeast-3", // Osaka
		"ap-southeast-1", // Singapore
		"ap-southeast-2", // Sydney
		"ap-south-1",     // Mumbai
		"ca-central-1",   // Central Canada
		"eu-central-1",   // Frankfurt
		"eu-west-1",      // Ireland
		"eu-west-2",      // London
		"eu-west-3",      // Paris
		"eu-north-1",     // Stockholm
		"me-south-1",     // Bahrain
		"sa-east-1",      // São Paulo
	}
}

func getGCPRegions() []string {
	return []string{
		"us-central1",          // Iowa
		"us-east1",             // South Carolina
		"us-east4",             // Northern Virginia
		"us-west1",             // Oregon
		"us-west2",             // Los Angeles
		"us-west3",             // Salt Lake City
		"us-west4",             // Las Vegas
		"europe-north1",        // Finland
		"europe-west1",         // Belgium
		"europe-west2",         // London
		"europe-west3",         // Frankfurt
		"europe-west4",         // Netherlands
		"europe-southwest1",    // Madrid
		"asia-northeast1",      // Tokyo
		"asia-northeast2",      // Osaka
		"asia-northeast3",      // Seoul
		"asia-south1",          // Mumbai
		"asia-southeast1",      // Singapore
		"asia-southeast2",      // Jakarta
		"asia-east1",           // Taiwan
		"australia-southeast1", // Sydney
		"australia-southeast2", // Melbourne
		"southamerica-east1",   // São Paulo
		"me-west1",             // UAE
	}
}

// createTempGCPCredentials creates a temporary file containing GCP credentials
func createTempGCPCredentials(creds GCPCloudCredentials) (string, error) {
	credJSON := map[string]string{
		"type":                        "service_account",
		"project_id":                  creds.ProjectID,
		"private_key_id":              creds.PrivateKeyID,
		"private_key":                 creds.PrivateKey,
		"client_email":                creds.ClientEmail,
		"client_id":                   creds.ClientID,
		"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
		"token_uri":                   "https://oauth2.googleapis.com/token",
		"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		"client_x509_cert_url":        fmt.Sprintf("https://www.googleapis.com/robot/v1/metadata/x509/%s", creds.ClientEmail),
	}

	tempFile, err := os.CreateTemp("", "gcp-creds-*.json")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	// err = json.NewEncoder(tempFile).Encode(creds)
	// if err != nil {
	// 	os.Remove(tempFile.Name())
	// 	return "", err
	// }
	encoder := json.NewEncoder(tempFile)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(credJSON); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write credentials to temp file: %v", err)
	}

	return tempFile.Name(), nil
}

func generateProviderConfig(account CloudAccount, outputDir string) error {
	var config strings.Builder

	switch account.Provider {
	case "aws":
		// AWS Provider configuration
		config.WriteString(`terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}
`)
	case "gcp":
		creds, ok := account.Credentials.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid GCP credentials format")
		}

		// GCP Provider configuration
		config.WriteString(fmt.Sprintf(`terraform {
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 4.0"
    }
  }
}

provider "google" {
  project = "%s"
  region  = var.gcp_region
}

variable "gcp_region" {
  description = "GCP region"
  type        = string
}
`, creds["project_id"]))
	}

	// Write the provider configuration to main.tf
	mainTfPath := filepath.Join(outputDir, "main.tf")
	return os.WriteFile(mainTfPath, []byte(config.String()), 0644)
}

// func removeDirRecursive(dirPath string) error {
// 	// Traverse and delete files and directories recursively
// 	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
// 		// If an error occurs, return it immediately
// 		if err != nil {
// 			return err
// 		}

// 		// If it's a file, delete it
// 		if !info.IsDir() {
// 			return os.Remove(path)
// 		} else {
// 			// If it's an empty directory, delete it
// 			return os.Remove(path)
// 		}
// 	})
// 	if err != nil {
// 		return err
// 	}

// 	// Finally, remove the directory itself
// 	return os.Remove(dirPath)
// }
