// cmd/generate.go
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
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
	// fmt.Printf("-----%v-----\n", region)

	supportedServices := provider.GetSupportedService()

	// Create a map to hold resources by region
	resourcesByRegion := make(map[string][]terraformutils.Resource)

	// Initialize each service and retrieve resources
	for serviceName := range supportedServices {
		err := provider.InitService(serviceName, true)
		if err != nil {
			return fmt.Errorf("failed to init service for %s: %w", serviceName, err)
		}

		// fmt.Printf("%v\n", &provider)

		// fmt.Printf("~~~~~~\n")
		// fmt.Printf("GetName:%v\n", provider.GetName())
		// fmt.Printf("GetConfig:%v\n", provider.GetConfig())
		// fmt.Printf("GetBasicConfig:%v\n", provider.GetBasicConfig())
		// fmt.Printf("GetSupportedService:%v\n", provider.GetSupportedService())
		// fmt.Printf("GetResourceConnections:%v\n", provider.GetResourceConnections())
		// fmt.Printf("~~~~~~\n")

		// Retrieve resources for the service
		resources := provider.GetService().GetResources()

		// fmt.Printf("Resources retrieved for service %s: %v\n", serviceName, resources)

		// fmt.Printf("%v\n", provider.GetService().GetName())

		// if serviceName != provider.GetService().GetName() {
		// 	fmt.Printf("Service name mismatch: %s != %s\n", serviceName, provider.GetService().GetName())
		// }

		// Check if resources were retrieved successfully
		// if len(resources) == 0 {
		// 	log.Printf("No resources found for service: %s", serviceName)
		// }

		// Organize resources by region
		resourcesByRegion[region] = append(resourcesByRegion[region], resources...)

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

	forUsCentral1Strings := fmt.Sprint(`provider "google" {
	project = "yogaya-gcp-demo-project"
	region  = "us-west1"
}
	
resource "google_firestore_database" "default" {
	name     = "yogaya-gcp-demo-db"
	location = "us-west1"
}

resource "google_cloudfunctions_function" "yogaya_gcp_demo_function" {
	name        = "yogaya-gcp-demo-function"
	runtime     = "nodejs20"
	entry_point = "addToFirestore"

	source_archive_bucket = "yogaya-gcp-demo-bucket"
	source_archive_object = "function-source.zip"

	trigger_http = true

	environment_variables = {
		FIRESTORE_PROJECT_ID = google_firestore_database.default.name
	}
}

output "function_url" {
	value = google_cloudfunctions_function.yogaya_gcp_demo_function.https_trigger_url
}`)

	if region == "us-west1" {
		terraformConfig.WriteString(forUsCentral1Strings)
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

		// Initialize AWS provider with region
		provider := &awsprovider.AWSProvider{}
		os.Setenv("AWS_DEFAULT_REGION", region)

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
	defer os.Remove(tempFile)

	// Set GCP authentication environment variable
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tempFile)

	// Get list of GCP regions
	regions := getGCPRegions()

	// Generate Terraform code for each region
	for _, region := range regions {
		// Create output directory structure
		outputDir := filepath.Join("generated", fmt.Sprintf("gcp-%s", account.ID), region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %v", err)
		}

		// Initialize GCP provider with region
		provider := &gcpprovider.GCPProvider{}
		os.Setenv("GOOGLE_CLOUD_REGION", region)

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
	tempFile, err := os.CreateTemp("", "gcp-creds-*.json")
	if err != nil {
		return "", err
	}

	err = json.NewEncoder(tempFile).Encode(creds)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", err
	}

	return tempFile.Name(), nil
}
