/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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

	// Setup global plugin directory and get cleanup function
	cleanup, err := ensureGlobalPluginDirectory()
	if err != nil {
		return fmt.Errorf("failed to setup plugin directory: %v", err)
	}
	// Execute cleanup on function completion
	defer func() {
		if err := cleanup(); err != nil {
			log.Printf("⚠️ Warning: Error during cleanup: %v", err)
		}
	}()

	regions := getAWSRegions()
	log.Printf("Processing %d AWS regions: %v", len(regions), regions)

	for i, region := range regions {
		log.Printf("Processing region %d/%d: %s", i+1, len(regions), region)
		// Create directory structure for region
		outputDir := fmt.Sprintf("generated/aws-%s/%s", account.ID, region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("error creating directory for region %s: %v", region, err)
		}
		log.Printf("Created output directory: %s", outputDir)

		// Create main.tf with provider configuration
		log.Printf("Creating main.tf for region %s", region)
		mainTFContent := fmt.Sprintf(`
terraform {
    required_providers {
        aws = {
            source  = "hashicorp/aws"
            version = "~> 4.0"
        }
    }
    required_version = ">= 0.13"
}

provider "aws" {
  region = "%s"
}
`, region)

		if err := os.WriteFile(fmt.Sprintf("%s/main.tf", outputDir), []byte(mainTFContent), 0644); err != nil {
			return fmt.Errorf("error writing main.tf: %v", err)
		}

		// Initialize Terraform first
		log.Printf("Running terraform init in %s", outputDir)
		terraformInitCmd := exec.Command("terraform", "init")
		terraformInitCmd.Dir = outputDir
		if output, err := terraformInitCmd.CombinedOutput(); err != nil {
			log.Printf("Terraform init output:\n%s", string(output))
			return fmt.Errorf("error running terraform init for AWS in region %s: %v", region, err)
		}
		log.Printf("✅ Terraform initialization successful for region %s", region)

		// Run Terraformer command for AWS
		log.Printf("Running Terraformer import for AWS region %s", region)
		terraformerImportCmd := exec.Command(terraformerPath, "import", "aws",
			"--resources=all",
			"--region", region,
			"--access-key", account.Credentials.(*AWSCredentials).AccessKeyID,
			"--secret-key", account.Credentials.(*AWSCredentials).SecretAccessKey)
		terraformerImportCmd.Dir = outputDir

		if output, err := terraformerImportCmd.CombinedOutput(); err != nil {
			log.Printf("Terraformer output:\n%s", string(output))
			return fmt.Errorf("error running Terraformer for AWS in region %s: %v", region, err)
		}
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

	// Setup global plugin directory and get cleanup function
	cleanup, err := ensureGlobalPluginDirectory()
	if err != nil {
		return fmt.Errorf("failed to setup plugin directory: %v", err)
	}
	defer func() {
		if err := cleanup(); err != nil {
			log.Printf("⚠️ Warning: Error during cleanup: %v", err)
		}
	}()

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

	for i, region := range regions {
		log.Printf("Processing region %d/%d: %s", i+1, len(regions), region)

		// Create directory structure for region
		outputDir := fmt.Sprintf("generated/gcp-%s/%s", account.ID, region)
		if err := os.MkdirAll(outputDir, 0755); err != nil {
			return fmt.Errorf("❌ error creating directory for region %s: %v", region, err)
		}
		log.Printf("Created output directory: %s", outputDir)

		// Create main.tf with provider configuration
		if err := createMainTF(outputDir, "google", gcpCloudCreds.ProjectID); err != nil {
			return fmt.Errorf("❌ error creating main.tf: %v", err)
		}
		log.Printf("✅ Created main.tf file for region %s", region)

		// Initialize Terraform
		log.Printf("Running terraform init in %s", outputDir)
		terraformInitCmd := exec.Command("terraform", "init")
		terraformInitCmd.Dir = outputDir
		if output, err := terraformInitCmd.CombinedOutput(); err != nil {
			log.Printf("Terraform init output:\n%s", string(output))
			return fmt.Errorf("❌ error running terraform init for GCP in region %s: %v", region, err)
		}
		log.Printf("✅ Terraform initialization successful for region %s", region)

		// Run Terraformer command
		log.Printf("Running Terraformer import for GCP region %s", region)

		pluginDir := filepath.Join(outputDir, ".terraform", "plugins")
		if err := os.MkdirAll(pluginDir, 0755); err != nil {
			return fmt.Errorf("error creating plugin directory: %v", err)
		}

		terraformImportCmd := exec.Command(terraformerPath, "import", "google",
			"--resources=all",
			"--regions="+region,
			"--projects="+gcpCloudCreds.ProjectID)

		terraformImportCmd.Dir = outputDir
		terraformImportCmd.Env = append(os.Environ(),
			"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
			"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID,
			"TF_PLUGIN_DIR="+pluginDir)

		if output, err := terraformImportCmd.CombinedOutput(); err != nil {
			log.Printf("Terraformer output:\n%s", string(output))
			return fmt.Errorf("❌ error running Terraformer for GCP in region %s: %v", region, err)
		}
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
func createMainTF(dir, provider, regionOrProject string) error {
	var mainTFContent string

	switch provider {
	case "google":
		mainTFContent = fmt.Sprintf(`
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
      version = "~> 4.0"
    }
  }
  required_version = ">= 0.13"
}

provider "google" {
  project = "%s"
  region  = "%s"
}
`, regionOrProject, strings.Split(dir, "/")[2]) // dir format is "generated/gcp-{accountID}/{region}"
	case "aws":
		mainTFContent = fmt.Sprintf(`
provider "aws" {
  region = "%s"
}
`, regionOrProject)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	err := os.WriteFile(fmt.Sprintf("%s/main.tf", dir), []byte(mainTFContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing main.tf: %v", err)
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
