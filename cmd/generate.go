// cmd/generate.go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	awsprovider "github.com/GoogleCloudPlatform/terraformer/providers/aws"
	gcpprovider "github.com/GoogleCloudPlatform/terraformer/providers/gcp"
	"github.com/GoogleCloudPlatform/terraformer/terraformutils"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/cobra"
	"github.com/zclconf/go-cty/cty"
	cloudresourcemanager "google.golang.org/api/cloudresourcemanager/v3"
	"google.golang.org/api/option"
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

// ProviderDetails holds provider-specific configuration details
type ProviderDetails struct {
	// Common fields
	Provider  string
	Region    string
	OutputDir string
	AccountID string

	// GCP specific fields
	ProjectID      string
	DisplayName    string
	ProjectNumber  string
	CredentialFile string

	// AWS specific fields
	AccessKeyID     string
	SecretAccessKey string
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
	// case "gcp":
	case "gcp":
		return processGCPAccount(account)
	default:
		return fmt.Errorf("unsupported provider: %s", account.Provider)
	}
}

// generateTerraformCode generates Terraform configuration for the specified provider
func generateTerraformCode(provider terraformutils.ProviderGenerator, region, outputDir string) error {
	supportedServices := provider.GetSupportedService()
	// fmt.Printf("Supported services count: %d\n", len(supportedServices))

	// Debug: Display list of supported services
	// fmt.Println("Supported services:")
	// for serviceName := range supportedServices {
	// 	fmt.Printf("- %s\n", serviceName)
	// }

	// Check authentication information
	// switch provider.GetName() {
	// case "google", "gcp", "google-beta":
	// 	fmt.Printf("GCP Project Name: %s\n", os.Getenv("GOOGLE_CLOUD_PROJECT"))
	// 	fmt.Printf("GCP Credentials Path: %s\n", os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	// 	// Check if credentials file exists
	// 	if _, err := os.Stat(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")); err != nil {
	// 		fmt.Printf("Credentials file error: %v\n", err)
	// 	}
	// case "aws":
	// 	fmt.Printf("AWS Region: %s\n", os.Getenv("AWS_DEFAULT_REGION"))
	// 	fmt.Printf("AWS Access Key ID: %s\n", os.Getenv("AWS_ACCESS_KEY_ID"))
	// 	fmt.Printf("AWS Secret Access Key: %s\n", os.Getenv("AWS_SECRET_ACCESS_KEY"))
	// default:
	// 	fmt.Printf("Unknown provider: %s\n", provider.GetName())
	// }

	// Create a map to hold resources by region
	resourcesByRegion := make(map[string][]terraformutils.Resource)
	existingResourceFlag := false

	// Initialize each service and retrieve resources
	for serviceName := range supportedServices {
		// fmt.Printf("\nTrying to initialize service: %s\n", serviceName)

		err := provider.InitService(serviceName, true)
		if err != nil {
			fmt.Printf("Error initializing service %s: %v\n", serviceName, err)
			continue
		}
		// fmt.Printf("Service initialized successfully: %s\n", serviceName)

		// Retrieve resources for the service
		service := provider.GetService()
		if service == nil {
			fmt.Printf("Service is nil for: %s\n", serviceName)
			continue
		}

		// Debug: Output information before GetResources
		// fmt.Printf("Attempting to get resources for service: %s\n", serviceName)

		resources := service.GetResources()

		// Debug: Check GetResources results
		// fmt.Printf("Resources retrieved for %s: %d\n", serviceName, len(resources))

		// Check if resources were retrieved successfully
		if len(resources) > 0 {
			fmt.Printf("Found %d resources for service: %s\n", len(resources), serviceName)
			existingResourceFlag = true

			// Debug output for resource details
			for _, r := range resources {
				fmt.Printf("  Resource Type: %s, Name: %s\n", r.InstanceInfo.Type, r.ResourceName)
				// Display resource attributes
				fmt.Printf("  Attributes:\n")
				for key, value := range r.Item {
					fmt.Printf("    %s: %v\n", key, value)
				}
			}
		}
		// else {
		// 	fmt.Printf("No resources found for service: %s\n", serviceName)
		// }

		// Organize resources by region
		resourcesByRegion[region] = append(resourcesByRegion[region], resources...)
	}

	// Remove empty directories if no resources found
	if !existingResourceFlag {
		fmt.Printf("No resources found in any service for region: %s\n", region)
		// err := os.RemoveAll(outputDir)
		// if err != nil {
		// 	fmt.Printf("Failed to remove directory: %v\n", err)
		// }
		// return nil
	}

	// Write resources to Terraform config files by region
	for region, resources := range resourcesByRegion {
		// fmt.Printf("Writing %d resources for region %s\n", len(resources), region)
		if err := writeTerraformConfig(resources, outputDir, region, provider); err != nil {
			return fmt.Errorf("failed to write Terraform config for region %s: %w", region, err)
		}
	}

	return nil
}

// writeTerraformConfig generates a Terraform configuration file from the resources for a specific region
func writeTerraformConfig(resources []terraformutils.Resource, outputDir, region string, provider terraformutils.ProviderGenerator) error {
	fmt.Printf("Starting to write Terraform config for region %s\n", region)

	// Create the output directory
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory: %v", err)
	}

	// Create a new HCL file
	f := hclwrite.NewEmptyFile()

	// Add provider configuration based on the provider type
	providerBlock := f.Body().AppendNewBlock("provider", []string{provider.GetName()})
	providerBody := providerBlock.Body()

	switch provider.GetName() {
	case "google", "gcp", "google-beta":
		projectName := os.Getenv("GOOGLE_CLOUD_PROJECT")
		// fmt.Printf("Setting GCP provider with Project ID: %s and Region: %s\n", projectName, region)
		providerBody.SetAttributeValue("project", cty.StringVal(projectName))
		providerBody.SetAttributeValue("region", cty.StringVal(region))
	case "aws":
		// fmt.Printf("Setting AWS provider with Region: %s\n", region)
		providerBody.SetAttributeValue("region", cty.StringVal(region))
	}

	// Add each resource to the HCL file
	resourceCount := 0
	for _, resource := range resources {
		fmt.Printf("Processing resource: Type=%s, Name=%s\n", resource.InstanceInfo.Type, resource.ResourceName)
		resourceBlock := f.Body().AppendNewBlock("resource", []string{resource.InstanceInfo.Type, resource.ResourceName})
		resourceBody := resourceBlock.Body()

		// Convert resource attributes to HCL
		attributeCount := 0
		for key, value := range resource.Item {
			attributeValue := convertToHCLValue(value)
			if attributeValue != cty.NilVal {
				resourceBody.SetAttributeValue(key, attributeValue)
				attributeCount++
			}
		}
		fmt.Printf("Added %d attributes to resource %s\n", attributeCount, resource.ResourceName)
		resourceCount++
	}

	// Set the output file name for the region's resources
	outputFile := filepath.Join(outputDir, fmt.Sprintf("%s_resources.tf", region))
	// fmt.Printf("Writing %d resources to file: %s\n", resourceCount, outputFile)

	if err := os.WriteFile(outputFile, f.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write to Terraform file: %v", err)
	}

	fmt.Printf("Successfully wrote Terraform config for region %s\n", region)
	fmt.Printf("~~~~~\n")
	return nil
}

// convertToHCLValue converts Go values to HCL-compatible cty.Value
func convertToHCLValue(value interface{}) cty.Value {
	switch v := value.(type) {
	case string:
		return cty.StringVal(v)
	case bool:
		return cty.BoolVal(v)
	case int:
		return cty.NumberIntVal(int64(v))
	case float64:
		return cty.NumberFloatVal(v)
	case []interface{}:
		elements := make([]cty.Value, 0, len(v))
		for _, element := range v {
			converted := convertToHCLValue(element)
			if converted != cty.NilVal {
				elements = append(elements, converted)
			}
		}
		if len(elements) > 0 {
			// Try to create a tuple if all elements are of the same type
			return cty.ListVal(elements)
		}
		return cty.NilVal
	case map[string]interface{}:
		attributes := make(map[string]cty.Value)
		for key, mapValue := range v {
			converted := convertToHCLValue(mapValue)
			if converted != cty.NilVal {
				attributes[key] = converted
			}
		}
		if len(attributes) > 0 {
			return cty.ObjectVal(attributes)
		}
		return cty.NilVal
	default:
		return cty.NilVal
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

		// Prepare provider details
		providerDetails := &ProviderDetails{
			Provider:        "aws",
			Region:          region,
			OutputDir:       outputDir,
			AccountID:       account.ID,
			AccessKeyID:     awsCreds.AccessKeyID,
			SecretAccessKey: awsCreds.SecretAccessKey,
		}

		// Generate provider configuration
		if err := generateProviderConfig(providerDetails); err != nil {
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
			region,    // args[0]: region
			"default", // args[1]: profile name
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

	// Get complete project details
	projectDetails, err := getGCPProjectDetails(gcpCreds.ProjectID, tempFile)
	if err != nil {
		return fmt.Errorf("failed to get project details: %v", err)
	}

	// Debug output
	fmt.Printf("GCP Project Details:\n")
	fmt.Printf("Project ID: %s\n", projectDetails.ProjectID)
	fmt.Printf("Project Display Name: %s\n", projectDetails.DisplayName)
	fmt.Printf("Project Number: %s\n\n", projectDetails.ProjectNumber)

	// Set GCP authentication environment variables
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", tempFile)
	os.Setenv("GOOGLE_CLOUD_PROJECT", projectDetails.DisplayName)

	// Get list of GCP regions
	regions := getGCPRegions()

	// Generate Terraform code for each region
	for _, region := range regions {
		// Create output directory structure
		outputDir := filepath.Join("generated", fmt.Sprintf("gcp-%s", account.ID), region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %v", err)
		}

		// Update provider details with region-specific information
		providerDetails := *projectDetails // Create a copy of the base details
		providerDetails.Region = region
		providerDetails.OutputDir = outputDir
		providerDetails.AccountID = account.ID

		// Generate provider configuration
		if err := generateProviderConfig(&providerDetails); err != nil {
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

		// Initialize provider with project details
		err := provider.Init([]string{
			region,                     // args[0]: Region
			projectDetails.DisplayName, // args[1]: ProjectName
			"",                         // args[2]: Empty for default provider type
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

// getGCPProjectDetails retrieves project details from project ID using Cloud Resource Manager API v3
func getGCPProjectDetails(projectID string, credentialsFile string) (*ProviderDetails, error) {
	ctx := context.Background()

	crmService, err := cloudresourcemanager.NewService(ctx, option.WithCredentialsFile(credentialsFile))
	if err != nil {
		return nil, fmt.Errorf("failed to create Cloud Resource Manager service: %v", err)
	}

	projectPath := fmt.Sprintf("projects/%s", projectID)
	project, err := crmService.Projects.Get(projectPath).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get project details: %v", err)
	}

	projectNumber := strings.TrimPrefix(project.Name, "projects/")

	return &ProviderDetails{
		Provider:       "gcp",
		ProjectID:      projectID,
		DisplayName:    project.DisplayName,
		ProjectNumber:  projectNumber,
		CredentialFile: credentialsFile,
	}, nil
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

// generateProviderConfig generates provider-specific Terraform configuration
func generateProviderConfig(details *ProviderDetails) error {
	var config strings.Builder

	switch details.Provider {
	case "aws":
		// AWS Provider configuration for Terraform 0.13
		config.WriteString(`terraform {
  required_version = ">= 0.13.0"
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  access_key = var.aws_access_key
  secret_key = var.aws_secret_key
  region     = var.aws_region
}

variable "aws_region" {
  description = "AWS region"
  type        = string
}

variable "aws_access_key" {
  description = "AWS access key"
  type        = string
  sensitive   = true
}

variable "aws_secret_key" {
  description = "AWS secret key"
  type        = string
  sensitive   = true
}
`)

		// Add tfvars file with credentials
		tfvars := fmt.Sprintf(`aws_access_key = "%s"
aws_secret_key = "%s"
aws_region     = "%s"
`, details.AccessKeyID, details.SecretAccessKey, details.Region)

		if err := os.WriteFile(filepath.Join(details.OutputDir, "terraform.tfvars"), []byte(tfvars), 0600); err != nil {
			return fmt.Errorf("failed to write tfvars file: %v", err)
		}

	case "gcp":
		// GCP Provider configuration for Terraform 0.13
		config.WriteString(fmt.Sprintf(`terraform {
  required_version = ">= 0.13.0"
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
}

provider "google" {
  credentials = "%s"
  project     = "%s"
  region      = var.gcp_region
}

# Project Information (for reference)
# Project Display Name: %s
# Project Number: %s

variable "gcp_region" {
  description = "GCP region"
  type        = string
}
`, details.CredentialFile, details.ProjectID, details.DisplayName, details.ProjectNumber))

		// Add tfvars file for GCP
		tfvars := fmt.Sprintf(`gcp_region = "%s"
`, details.Region)

		if err := os.WriteFile(filepath.Join(details.OutputDir, "terraform.tfvars"), []byte(tfvars), 0600); err != nil {
			return fmt.Errorf("failed to write tfvars file: %v", err)
		}
	}

	// Write the provider configuration to main.tf
	mainTfPath := filepath.Join(details.OutputDir, "main.tf")
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
