/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/spf13/cobra"
)

// generateCmd represents the generate command
var generateCmd = &cobra.Command{
	Use:   "generate [.sample/cloud_accounts.conf-file-path]",
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
		fmt.Println("Usage: sample generate <.sample/cloud_accounts.conf-file-path>")
		return
	}

	credFilePath := args[0]
	log.Printf("Starting Terraform code generation using credentials from: %s", credFilePath)

	// Load the credentials file
	cm, err := NewCredentialManager(credFilePath)
	if err != nil {
		log.Fatalf("❌ Error initializing credential manager: %v", err)
		return
	}
	log.Printf("✅ Successfully loaded credentials for %d accounts", len(cm.config.Accounts))

	// Iterate over each cloud account and run Terraformer
	for i, account := range cm.config.Accounts {
		log.Printf("Processing account %d/%d: %s (%s)", i+1, len(cm.config.Accounts), account.ID, account.Provider)

		switch account.Provider {
		case "aws":
			if err := runTerraformerAWS(account); err != nil {
				log.Printf("❌ Error generating Terraform code for AWS account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for AWS account %s", account.ID)
			}
		case "gcp":
			if err := runTerraformerGCP(account); err != nil {
				log.Printf("❌ Error generating Terraform code for GCP account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for GCP account %s", account.ID)
			}
		default:
			log.Printf("⚠️ Skipping unsupported provider: %s", account.Provider)
		}
	}
	log.Println("Generation process completed")
}

func ensureGlobalPluginDirectory() (func() error, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("error getting home directory: %v", err)
	}

	// Create platform-specific plugin directory path
	platformDir := "darwin_arm64" // This should be determined based on the current platform
	pluginDir := filepath.Join(homeDir, ".terraform.d", "plugins", platformDir)
	backupDir := filepath.Join(homeDir, ".terraform.d", "plugins.backup", platformDir)

	// Define cleanup function
	cleanup := func() error {
		if _, err := os.Stat(backupDir); err == nil {
			log.Printf("Restoring plugin directory from backup: %s", backupDir)

			// Remove current plugin directory and its parent if empty
			if err := os.RemoveAll(filepath.Dir(pluginDir)); err != nil {
				return fmt.Errorf("error removing current plugin directory: %v", err)
			}

			// Ensure parent directory exists for restore
			if err := os.MkdirAll(filepath.Dir(pluginDir), 0755); err != nil {
				return fmt.Errorf("error creating parent plugin directory: %v", err)
			}

			// Move backup back to original location
			if err := os.Rename(backupDir, pluginDir); err != nil {
				return fmt.Errorf("error restoring plugin directory from backup: %v", err)
			}

			// Remove backup parent directory if empty
			os.Remove(filepath.Dir(backupDir)) // Ignore error if not empty
			log.Println("✅ Plugin directory successfully restored")
		}
		return nil
	}

	// Check if directory exists
	if _, err := os.Stat(pluginDir); err == nil {
		log.Printf("Creating backup of existing plugin directory: %s", backupDir)

		if err := os.MkdirAll(filepath.Dir(backupDir), 0755); err != nil {
			return nil, fmt.Errorf("error creating backup parent directory: %v", err)
		}

		if _, err := os.Stat(backupDir); err == nil {
			if err := os.RemoveAll(backupDir); err != nil {
				return nil, fmt.Errorf("error removing existing backup directory: %v", err)
			}
		}

		if err := os.Rename(pluginDir, backupDir); err != nil {
			return nil, fmt.Errorf("error creating backup of plugin directory: %v", err)
		}
		log.Println("✅ Plugin directory backup created successfully")
	}

	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		return cleanup, fmt.Errorf("error creating plugin directory: %v", err)
	}
	log.Println("✅ Global plugin directory setup completed")

	return cleanup, nil

}

// runTerraformerAWS executes Terraformer for AWS to generate resources for each region
func runTerraformerAWS(account CloudAccount) error {
	log.Printf("Starting AWS Terraformer process for account: %s", account.ID)

	// Get current working directory (project root)
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting current working directory: %v", err)
	}

	// Define terraformer binary path relative to project root
	terraformerPath := filepath.Join(projectDir, "bin", "terraformer")
	log.Printf("Using Terraformer binary at: %s", terraformerPath)

	// Check if terraformer binary exists
	if _, err := os.Stat(terraformerPath); err != nil {
		return fmt.Errorf("❌ terraformer binary not found at %s: %v", terraformerPath, err)
	}

	// Make sure the binary is executable
	if err := os.Chmod(terraformerPath, 0755); err != nil {
		return fmt.Errorf("❌ failed to make terraformer binary executable: %v", err)
	}
	log.Println("✅ Terraformer binary verified and executable")

	// Process AWS credentials
	log.Println("Processing AWS credentials...")
	credMap, ok := account.Credentials.(map[string]interface{})
	if !ok {
		return fmt.Errorf("❌ invalid credentials type for AWS account %s", account.ID)
	}

	// Extract AWS credentials from the map
	accessKeyID, ok := credMap["access_key_id"].(string)
	if !ok {
		return fmt.Errorf("❌ invalid or missing access_key_id for AWS account %s", account.ID)
	}

	secretAccessKey, ok := credMap["secret_access_key"].(string)
	if !ok {
		return fmt.Errorf("❌ invalid or missing secret_access_key for AWS account %s", account.ID)
	}

	log.Println("✅ AWS credentials processed successfully")

	// Create base output directory
	baseOutputDir := fmt.Sprintf("generated/aws-%s", account.ID)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	// Create global main.tf for shared configurations
	if err := createMainTF("aws", baseOutputDir, ""); err != nil {
		return fmt.Errorf("error writing global main.tf: %v", err)
	}

	// Get AWS regions
	regions := getAWSRegions()
	log.Printf("Processing %d AWS regions: %v", len(regions), regions)

	// Initialize Terraform in base directory
	log.Printf("Running terraform init in %s", baseOutputDir)
	terraformInitCmd := exec.Command("terraform", "init")
	terraformInitCmd.Dir = baseOutputDir
	output, err := terraformInitCmd.CombinedOutput()
	if err != nil {
		log.Printf("Terraform init output:\n%s", string(output))
		return fmt.Errorf("error running terraform init: %v", err)
	}
	log.Printf("✅ Terraform initialization successful")

	// Process each region
	for i, region := range regions {
		log.Printf("Processing region %d/%d: %s", i+1, len(regions), region)

		// Get available services for this region
		services := getAvailableServicesForRegion(region)
		if len(services) == 0 {
			log.Printf("⚠️ No services configured for region %s, skipping", region)
			continue
		}
		log.Printf("Found %d available services in region %s", len(services), region)

		// Create directory structure for region
		regionDir := filepath.Join(baseOutputDir, region)
		if err := os.MkdirAll(regionDir, 0755); err != nil {
			return fmt.Errorf("error creating directory for region %s: %v", region, err)
		}
		log.Printf("Created output directory: %s", regionDir)

		// Run Terraformer command for AWS region
		log.Printf("Running Terraformer import for AWS region %s", region)
		terraformerImportCmd := exec.Command(terraformerPath, "import", "aws",
			"--resources=", strings.Join(services, ","),
			"--regions", region,
			"--access-key", accessKeyID,
			"--secret-key", secretAccessKey,
			"--path-output="+"./"+region)

		terraformerImportCmd.Dir = baseOutputDir
		terraformerImportCmd.Env = append(os.Environ(),
			"AWS_ACCESS_KEY_ID="+accessKeyID,
			"AWS_SECRET_ACCESS_KEY="+secretAccessKey)

		if output, err := terraformerImportCmd.CombinedOutput(); err != nil {
			log.Printf("Terraformer import output for region %s:\n%s", region, string(output))
			log.Printf("⚠️ Error in region %s: %v", region, err)
			continue
		}
		// If there seems to be a problem with Terraformer itself, enable it.
		// log.Printf("Terraformer import output:\n%s", string(output))
		log.Printf("✅ Successfully generated Terraform code for region %s", region)
	}
	log.Printf("✅ Completed AWS Terraformer process for account: %s", account.ID)
	return nil
}

// runTerraformerGCP executes Terraformer for GCP to generate resources for each region
func runTerraformerGCP(account CloudAccount) error {
	log.Printf("Starting GCP Terraformer process for account: %s", account.ID)

	// Get current working directory (project root)
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("error getting current working directory: %v", err)
	}

	// Define terraformer binary path relative to project root
	terraformerPath := filepath.Join(projectDir, "bin", "terraformer")
	log.Printf("Using Terraformer binary at: %s", terraformerPath)

	// Check if terraformer binary exists
	if _, err := os.Stat(terraformerPath); err != nil {
		return fmt.Errorf("❌ terraformer binary not found at %s: %v", terraformerPath, err)
	}

	// Make sure the binary is executable
	if err := os.Chmod(terraformerPath, 0755); err != nil {
		return fmt.Errorf("❌ failed to make terraformer binary executable: %v", err)
	}
	log.Println("✅ Terraformer binary verified and executable")

	// Process GCP credentials
	log.Println("Processing GCP credentials...")
	gcpCreds, ok := account.Credentials.(map[string]interface{})
	if !ok {
		return fmt.Errorf("❌ invalid credentials type for GCP account %s", account.ID)
	}

	var gcpCloudCreds GCPCloudCredentials
	credBytes, err := json.Marshal(gcpCreds)
	if err != nil {
		return fmt.Errorf("❌ failed to marshal GCP credentials: %v", err)
	}

	err = json.Unmarshal(credBytes, &gcpCloudCreds)
	if err != nil {
		return fmt.Errorf("❌ failed to unmarshal GCP credentials: %v", err)
	}
	log.Println("✅ GCP credentials processed successfully")

	// Create a temporary credentials file
	log.Println("Creating temporary credentials file...")
	tempFile, err := os.CreateTemp("", "gcp-credentials-*.json")
	if err != nil {
		return fmt.Errorf("❌ error creating temporary credentials file: %v", err)
	}
	defer func() {
		if err := os.Remove(tempFile.Name()); err != nil {
			log.Printf("⚠️ Warning: Failed to remove temporary credentials file: %v", err)
		} else {
			log.Println("✅ Temporary credentials file cleaned up successfully")
		}
	}()

	baseOutputDir := fmt.Sprintf("generated/gcp-%s", account.ID)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	if err := createMainTF("gcp", baseOutputDir, gcpCloudCreds.ProjectID); err != nil {
		return fmt.Errorf("error writing global main.tf: %v", err)
	}

	// Write credentials to temporary file
	gcpCredsJSON := fmt.Sprintf(`{
        "type": "service_account",
        "project_id": "%s",
        "private_key_id": "%s",
        "private_key": "%s",
        "client_email": "%s",
        "client_id": "%s",
        "auth_uri": "https://accounts.google.com/o/oauth2/auth",
        "token_uri": "https://oauth2.googleapis.com/token",
        "auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
        "client_x509_cert_url": "https://www.googleapis.com/robot/v1/metadata/x509/%s"
    }`, gcpCloudCreds.ProjectID, gcpCloudCreds.PrivateKeyID, escapeNewlines(gcpCloudCreds.PrivateKey),
		gcpCloudCreds.ClientEmail, gcpCloudCreds.ClientID, gcpCloudCreds.ClientEmail)

	if err := os.WriteFile(tempFile.Name(), []byte(gcpCredsJSON), 0600); err != nil {
		return fmt.Errorf("❌ error writing GCP credentials to temporary file: %v", err)
	}
	log.Printf("✅ Created temporary credentials file at: %s", tempFile.Name())

	regions := getGCPRegions()
	log.Printf("Processing %d GCP regions: %v", len(regions), regions)

	// Initialize Terraform in base directory
	log.Printf("Running terraform init in %s", baseOutputDir)
	terraformInitCmd := exec.Command("terraform", "init")
	terraformInitCmd.Dir = baseOutputDir
	output, err := terraformInitCmd.CombinedOutput()
	if err != nil {
		log.Printf("Terraform init output:\n%s", string(output))
		return fmt.Errorf("error running terraform init: %v", err)
	}
	// If there seems to be a problem with Terraform itself, enable it.
	// log.Printf("Terraform init output:\n%s", string(output))
	log.Printf("✅ Terraform initialization successful")

	for i, region := range regions {
		log.Printf("Processing region %d/%d: %s", i+1, len(regions), region)

		// Create directory structure for region
		regionDir := filepath.Join(baseOutputDir, region)
		if err := os.MkdirAll(regionDir, 0755); err != nil {
			return fmt.Errorf("error creating directory for region %s: %v", region, err)
		}
		log.Printf("Created output directory: %s", regionDir)

		// Run Terraformer command
		log.Printf("Running Terraformer import for GCP region %s", region)

		terraformerImportCmd := exec.Command(terraformerPath, "import", "google",
			"--resources=\"*\"",
			"--regions="+region,
			"--projects="+gcpCloudCreds.ProjectID,
			"--path-output="+"./"+region)

		terraformerImportCmd.Dir = baseOutputDir
		terraformerImportCmd.Env = append(os.Environ(),
			"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
			"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID)

		output, err := terraformerImportCmd.CombinedOutput()
		if err != nil {
			log.Printf("Terraformer import output:\n%s", string(output))
			return fmt.Errorf("❌ error running Terraformer for GCP in region %s: %v", region, err)
		}
		// If there seems to be a problem with Terraformer itself, enable it.
		// log.Printf("Terraformer import output:\n%s", string(output))
		log.Printf("✅ Successfully generated Terraform code for region %s", region)
	}

	log.Printf("✅ Completed GCP Terraformer process for account: %s", account.ID)
	return nil
}

// escapeNewlines escapes newlines in the private key so that it can be inserted into a JSON string
func escapeNewlines(input string) string {
	return strings.ReplaceAll(input, "\n", "\\n")
}

// createMainTF creates the main.tf file for a cloud provider
func createMainTF(provider, dir, fileAttributes string) error {
	var mainTFContent string

	switch provider {
	case "gcp":
		mainTFContent = fmt.Sprintf(`
terraform {
	required_providers {
		google = {
			source  = "hashicorp/google"
			version = "~> 4.0.0"
		}
	}
	required_version = ">= 1.9.8"
}

provider "google" {
	project = "%s"
}
`, fileAttributes)
	case "aws":
		mainTFContent = fmt.Sprintf(`
provider "aws" {
  region = "%s"
}
`, fileAttributes)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	err := os.WriteFile(fmt.Sprintf("%s/main.tf", dir), []byte(mainTFContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing main.tf: %v", err)
	}

	return nil
}

// isValidRegionFormat checks if the region string matches the specified cloud provider's format
func isValidRegionFormat(provider, region string) bool {
	var pattern string
	switch provider {
	case "aws":
		// AWS region format: us-east-1, eu-west-2, ap-southeast-1
		pattern = `^[a-z]{2}-[a-z]+-\d{1}$`
	case "gcp":
		// GCP region format: us-central1, europe-west4, asia-east1
		pattern = `^[a-z]+-[a-z]+\d{1}$`
	default:
		return false
	}
	match, _ := regexp.MatchString(pattern, region)
	return match
}

// getAWSRegions is a wrapper function that tries different methods to get regions
func getAWSRegions() []string {
	// Try fetching regions dynamically first
	regions, err := fetchAWSRegions()
	if err != nil || len(regions) == 0 {
		// Return hardcoded list as last resort
		return []string{
			"us-east-1",
			"us-east-2",
			"us-west-1",
			"us-west-2",
			"af-south-1",
			"ap-east-1",
			"ap-northeast-1",
			"ap-northeast-2",
			"ap-northeast-3",
			"ap-southeast-1",
			"ap-southeast-2",
			"ap-south-1",
			"ca-central-1",
			"eu-central-1",
			"eu-west-1",
			"eu-west-2",
			"eu-west-3",
			"eu-north-1",
			"me-south-1",
			"sa-east-1",
		}
	}

	return regions
}

// fetchAWSRegions retrieves the list of available AWS regions dynamically
func fetchAWSRegions() ([]string, error) {
	// AWS regions documentation URL
	url := "https://docs.aws.amazon.com/general/latest/gr/rande.html"

	// Send HTTP GET request
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch AWS regions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch AWS regions: received status code %d", resp.StatusCode)
	}

	// Parse HTML using goquery
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	// Use a map to store unique regions and avoid duplicates
	regionMap := make(map[string]bool)

	// Find and process region information
	doc.Find("div.table-container table tbody tr").Each(func(i int, s *goquery.Selection) {
		// Extract region code from the table cell
		region := s.Find("td:nth-child(2)").Text()
		region = strings.TrimSpace(region)

		// Validate region format using regex
		if isValidRegionFormat("aws", region) {
			regionMap[region] = true
		}
	})

	// Convert map to sorted slice
	regions := make([]string, 0, len(regionMap))
	for region := range regionMap {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// Validate results
	if len(regions) == 0 {
		return nil, fmt.Errorf("no valid AWS regions found")
	}

	log.Printf("Found %d AWS regions", len(regions))
	return regions, nil
}

// getGCPRegions is a wrapper function that tries different methods to get regions
func getGCPRegions() []string {
	// Try fetching regions dynamically first
	regions, err := fetchGCPRegions()
	if err != nil || len(regions) == 0 {
		// Return hardcoded list as last resort
		return []string{
			"asia-east1",
			"asia-east2",
			"asia-northeast1",
			"asia-northeast2",
			"asia-northeast3",
			"asia-south1",
			"asia-southeast1",
			"australia-southeast1",
			"europe-central2",
			"europe-north1",
			"europe-west1",
			"europe-west2",
			"europe-west3",
			"europe-west4",
			"europe-west6",
			"northamerica-northeast1",
			"southamerica-east1",
			"us-central1",
			"us-east1",
			"us-east4",
			"us-west1",
			"us-west2",
			"us-west3",
			"us-west4",
		}
	}

	return regions
}

// fetchGCPRegions retrieves the list of available GCP regions dynamically
func fetchGCPRegions() ([]string, error) {
	// GCP regions documentation URL
	url := "https://cloud.google.com/compute/docs/regions-zones"

	// Send HTTP GET request
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch GCP regions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch GCP regions: received status code %d", resp.StatusCode)
	}

	// Parse HTML using goquery
	// Parse HTML using goquery
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse HTML: %v", err)
	}

	// Use a map to store unique regions and avoid duplicates
	regionMap := make(map[string]bool)

	// Select the table or element containing the region data
	doc.Find("table").Each(func(i int, s *goquery.Selection) {
		// Find table rows and extract region names
		s.Find("tr").Each(func(j int, row *goquery.Selection) {
			region := row.Find("td").First().Text()
			region = strings.TrimSpace(region)

			// Filter and validate regions
			if region != "" && !strings.Contains(region, "Zone") && isValidRegionFormat("gcp", region) {
				regionMap[region] = true
			}
		})
	})

	// Convert map to sorted slice
	regions := make([]string, 0, len(regionMap))
	for region := range regionMap {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// Validate results
	if len(regions) == 0 {
		return nil, fmt.Errorf("no valid GCP regions found")
	}

	log.Printf("Found %d GCP regions", len(regions))
	return regions, nil
}

// ServiceGroup defines a group of AWS services that can be imported
type ServiceGroup struct {
	Name     string
	Services []string
	Regions  []string // empty means available in all regions
}

// AWS Regions as of April 2024
var commercialRegions = []string{
	// Americas
	"us-east-1",    // US East (N. Virginia)
	"us-east-2",    // US East (Ohio)
	"us-west-1",    // US West (N. California)
	"us-west-2",    // US West (Oregon)
	"ca-central-1", // Canada (Central)
	"ca-west-1",    // Canada West (Calgary)
	"sa-east-1",    // South America (São Paulo)

	// Europe
	"eu-central-1", // Europe (Frankfurt)
	"eu-central-2", // Europe (Zurich)
	"eu-west-1",    // Europe (Ireland)
	"eu-west-2",    // Europe (London)
	"eu-west-3",    // Europe (Paris)
	"eu-south-1",   // Europe (Milan)
	"eu-south-2",   // Europe (Spain)
	"eu-north-1",   // Europe (Stockholm)

	// Asia Pacific
	"ap-east-1",      // Asia Pacific (Hong Kong)
	"ap-south-1",     // Asia Pacific (Mumbai)
	"ap-south-2",     // Asia Pacific (Hyderabad)
	"ap-southeast-1", // Asia Pacific (Singapore)
	"ap-southeast-2", // Asia Pacific (Sydney)
	"ap-southeast-3", // Asia Pacific (Jakarta)
	"ap-southeast-4", // Asia Pacific (Melbourne)
	"ap-northeast-1", // Asia Pacific (Tokyo)
	"ap-northeast-2", // Asia Pacific (Seoul)
	"ap-northeast-3", // Asia Pacific (Osaka)

	// Middle East and Africa
	"me-central-1", // Middle East (UAE)
	"me-south-1",   // Middle East (Bahrain)
	"af-south-1",   // Africa (Cape Town)

	// Special Regions
	"il-central-1", // Israel (Tel Aviv)
}

// Define service groups based on availability and characteristics for 2024
var awsServiceGroups = []ServiceGroup{
	{
		Name: "Core Infrastructure",
		Services: []string{
			"vpc", "subnet", "sg", "nacl", "rt", "igw", "nat",
			"ec2_instance", "ebs", "eip",
			"cloudwatch", "cloudtrail",
		},
		Regions: []string{}, // available in all regions
	},
	{
		Name: "Identity and Security",
		Services: []string{
			"iam", "acm", "kms", "secretsmanager",
		},
		Regions: []string{}, // Most are global services
	},
	{
		Name: "Compute and Containers",
		Services: []string{
			"auto_scaling", "lambda", "eks", "ecs", "ecr",
		},
		Regions: commercialRegions,
	},
	{
		Name: "Storage",
		Services: []string{
			"s3", "efs", "fsx",
		},
		Regions: commercialRegions,
	},
	{
		Name: "Database",
		Services: []string{
			"rds", "dynamodb", "elasticache",
			"docdb", "memorydb",
		},
		Regions: filterRegions(commercialRegions, []string{
			"ap-northeast-3", // Limited service availability
			"ap-southeast-3",
			"ap-southeast-4",
			"ap-south-2",
			"eu-south-2",
			"eu-central-2",
			"me-west-1",
			"ca-west-1",
		}),
	},
	{
		Name: "Network and Content Delivery",
		Services: []string{
			"alb", "elb", "cloudfront", "route53",
			"globalaccelerator", "api_gateway",
		},
		Regions: commercialRegions,
	},
	{
		Name: "Analytics and Messaging",
		Services: []string{
			"sns", "sqs", "kinesis", "msk",
			"emr",
		},
		Regions: filterRegions(commercialRegions, []string{
			"ap-northeast-3",
			"ap-southeast-3",
			"ap-southeast-4",
			"ap-south-2",
			"eu-south-2",
			"ca-west-1",
		}),
	},
	{
		Name: "Management and Governance",
		Services: []string{
			"cloudformation", "config", "organizations",
			"ssm", "backup",
		},
		Regions: commercialRegions,
	},
}

// filterRegions removes specified regions from the full list
func filterRegions(allRegions []string, excludeRegions []string) []string {
	excluded := make(map[string]bool)
	for _, r := range excludeRegions {
		excluded[r] = true
	}

	var filtered []string
	for _, r := range allRegions {
		if !excluded[r] {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// getAvailableServicesForRegion returns a list of services available in the specified region
func getAvailableServicesForRegion(region string) []string {
	var availableServices []string

	for _, group := range awsServiceGroups {
		// If Regions is empty or contains the specified region
		if len(group.Regions) == 0 || contains(group.Regions, region) {
			availableServices = append(availableServices, group.Services...)
		}
	}

	return uniqueStrings(availableServices)
}

// contains checks if a string slice contains a specific string
func contains(slice []string, str string) bool {
	for _, v := range slice {
		if v == str {
			return true
		}
	}
	return false
}

// uniqueStrings returns a new slice with duplicate strings removed
func uniqueStrings(slice []string) []string {
	keys := make(map[string]bool)
	var list []string
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
