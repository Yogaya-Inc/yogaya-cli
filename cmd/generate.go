/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

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

	homeDir, err := os.UserHomeDir()
	pluginDir := filepath.Join(homeDir, ".terraform.d", "plugins", "darwin_arm64")
	// Create directory with all parent directories if they don't exist
	err = os.MkdirAll(pluginDir, 0755)

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
		case "azure":
			if err := runTerraformerAzure(account); err != nil {
				log.Printf("❌ Error generating Terraform code for Azure account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for Azure account %s", account.ID)
			}
		default:
			log.Printf("⚠️ Skipping unsupported provider: %s", account.Provider)
		}
	}
	log.Println("Generation process completed")
}

// runTerraformerAWS executes Terraformer for AWS to generate resources for each region
func runTerraformerAWS(account CloudAccount) error {
	log.Printf("Starting process for account: %s", account.ID)

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

	outputCompletedServiceCount := 0

	// Get AWS regions
	regions := getAWSRegions()
	// log.Printf("Processing %d AWS regions: %v\n", len(regions), regions)

	// Define maximum number of concurrent workers
	maxConcurrency := 7 // Max Threads
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // To protect shared resources like log output
	errors := []error{}

	for i, region := range regions {
		wg.Add(1)
		go func(region string, index int) {
			defer wg.Done()

			// Acquire a slot in the semaphore
			sem <- struct{}{}
			defer func() { <-sem }() // Release the slot when done

			log.Printf("Processing %v region...\n", region)

			regionDir := filepath.Join(baseOutputDir, region)
			if err := os.MkdirAll(regionDir, 0755); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("error creating directory for region %s: %v", region, err))
				mu.Unlock()
				return
			}

			if err := createMainTF("aws", regionDir, region); err != nil {
				fmt.Printf("error writing global main.tf: %v", err)
				return
			}

			// log.Printf("Running terraform init in %s", regionDir)
			terraformInitCmd := exec.Command("terraform", "init")
			terraformInitCmd.Dir = regionDir
			initOutput, err := terraformInitCmd.CombinedOutput()
			if err != nil {
				log.Printf("Terraform init output:\n%s", string(initOutput))
				fmt.Printf("error running terraform init: %v", err)
				return
			}
			// If there seems to be a problem with Terraform itself, enable it.
			// log.Printf("Terraform init output:\n%s", string(initOutput))
			// log.Printf("✅ Terraform initialization successful")

			resources := getAvailableAWSServices()
			// if len(services) == 0 {
			// 	log.Printf("⚠️ No services configured for region %s, skipping", region)
			// 	return
			// }
			terraformerImportCmd := exec.Command("terraformer", "import", "aws",
				"--resources="+strings.Join(resources, ","),
				"--regions="+region,
				"--path-output=./")
			terraformerImportCmd.Dir = regionDir
			terraformerImportCmd.Env = append(os.Environ(),
				"AWS_ACCESS_KEY_ID="+accessKeyID,
				"AWS_SECRET_ACCESS_KEY="+secretAccessKey)

			importOutput, err := terraformerImportCmd.CombinedOutput()
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("error running Terraformer for region %s: %v\nOutput: %s", region, err, string(importOutput)))
				mu.Unlock()
				return
			}

			// for _, service := range services {
			// terraformerImportCmd := exec.Command(terraformerPath, "import", "aws",
			// 	"--resources="+service,
			// 	"--regions="+region,
			// 	"--path-output=./")
			// terraformerImportCmd.Dir = regionDir
			// terraformerImportCmd.Env = append(os.Environ(),
			// 	"AWS_ACCESS_KEY_ID="+accessKeyID,
			// 	"AWS_SECRET_ACCESS_KEY="+secretAccessKey)

			// importOutput, err := terraformerImportCmd.CombinedOutput()
			// if err != nil {
			// 	mu.Lock()
			// 	errors = append(errors, fmt.Errorf("error running Terraformer for region %s: %v\nOutput: %s", region, err, string(importOutput)))
			// 	mu.Unlock()
			// 	return
			// }
			// If there seems to be a problem with Terraformer itself, enable it.
			// log.Printf("Terraformer import output:\n%s", string(importOutput))
			// }

			// if err := mergeFilesOfRefion(baseOutputDir, "aws"); err != nil {
			// 	fmt.Printf("Internal error: %v\n", err)
			// }

			mergeFilesOfRefion(baseOutputDir, "aws")

			removedWorkDir(filepath.Join(baseOutputDir, "all_resources_in_aws-"+account.ID+".tf"), regionDir, "aws")

			os.RemoveAll(filepath.Join(regionDir, ".terraform"))
			os.Remove(filepath.Join(regionDir, "main.tf"))
			os.Remove(filepath.Join(regionDir, ".terraform.lock.hcl"))

			outputCompletedServiceCount++
			log.Printf("✅ Successfully generated Terraform code for region %s (%v/%v)", region, outputCompletedServiceCount, len(regions))
		}(region, i)
	}

	wg.Wait()

	// Handle errors after all regions are processed
	if len(errors) > 0 {
		return fmt.Errorf("encountered errors during AWS Terraformer process: %v", errors)
	}

	os.Remove("generated/all_resources_in_generated.tf")
	os.Remove("./all_resources_in_..tf")

	log.Printf("✅ Completed AWS Terraformer process for account: %s", account.ID)
	return nil
}

// runTerraformerGCP executes Terraformer for GCP to generate resources for each region
func runTerraformerGCP(account CloudAccount) error {
	log.Printf("Starting process for account: %s", account.ID)

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
	// log.Printf("✅ Created temporary credentials file at: %s", tempFile.Name())

	// Initialize Terraform in base directory
	// log.Printf("Running terraform init in %s", baseOutputDir)
	terraformInitCmd := exec.Command("terraform", "init")
	terraformInitCmd.Dir = baseOutputDir
	initOutput, err := terraformInitCmd.CombinedOutput()
	if err != nil {
		log.Printf("Terraform init output:\n%s", string(initOutput))
		return fmt.Errorf("error running terraform init: %v", err)
	}
	// If there seems to be a problem with Terraform itself, enable it.
	// log.Printf("Terraform init output:\n%s", string(initOutput))
	// log.Printf("✅ Terraform initialization successful")

	// regions := []string{"asia-southeast2"}
	terraformerImportRegionsListCmd := exec.Command("gcloud", "compute", "regions", "list")
	terraformerImportRegionsListCmd.Dir = baseOutputDir
	terraformerImportRegionsListCmd.Env = append(os.Environ(),
		"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
		"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID,
		"CLOUDSDK_CORE_PROJECT="+gcpCloudCreds.ProjectID)
	importRegionsListOutput, err := terraformerImportRegionsListCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Error in getting GCP services that Terraformer supports regions importing: Output: %s", importRegionsListOutput)
	}
	regions := getGCPRegions(importRegionsListOutput)
	// log.Printf("Processing %d GCP regions: %v", len(regions), regions)

	// Define maximum number of concurrent workers
	maxConcurrency := 5 // Max Threads
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // To protect shared resources like log output
	errors := []error{}

	outputCompletedServiceCount := 0

	terraformerImportResourcesListCmd := exec.Command("gcloud", "asset", "list", "--project="+gcpCloudCreds.ProjectID, "--format=json")
	terraformerImportResourcesListCmd.Dir = baseOutputDir
	terraformerImportResourcesListCmd.Env = append(os.Environ(),
		"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
		"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID)
	importListOutput, err := terraformerImportResourcesListCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("Error in getting GCP services that Terraformer supports importing: Output: %s", importListOutput)
	}

	var assets []Asset
	if err := json.Unmarshal(importListOutput, &assets); err != nil {
		fmt.Printf("Error parsing target resources name: %v\n", err)
	}

	resourceMap := make(map[string]struct{})

	for _, asset := range assets {
		if resource := convertToTerraformerResource(asset.AssetType); resource != "" {
			resourceMap[resource] = struct{}{}
		}
	}

	var resources []string
	for resource := range resourceMap {
		// if resource == "cloudFunctions" {
		// 	continue
		// }
		resources = append(resources, resource)
	}

	for i, region := range regions {
		wg.Add(1)
		go func(region string, index int) {
			defer wg.Done()

			// Acquire a slot in the semaphore
			sem <- struct{}{}
			defer func() { <-sem }() // Release the slot when done

			log.Printf("Processing %v region...\n", region)

			regionDir := filepath.Join(baseOutputDir, region)
			if err := os.MkdirAll(regionDir, 0755); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("error creating directory for region %s: %v", region, err))
				mu.Unlock()
				return
			}

			terraformerImportCmd := exec.Command("terraformer", "import", "google",
				"--resources="+strings.Join(resources, ","),
				"--regions="+region,
				"--projects="+gcpCloudCreds.ProjectID,
				"--path-output=./"+region)
			terraformerImportCmd.Dir = baseOutputDir
			terraformerImportCmd.Env = append(os.Environ(),
				"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
				"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID)

			importOutput, err := terraformerImportCmd.CombinedOutput()
			// fmt.Printf("importOutput\n%v", string(importOutput))
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("error running Terraformer for GCP region %s: %v\nOutput: %s", region, err, string(importOutput)))
				mu.Unlock()
				return
			}

			// for _, service := range services {
			// 	if service == "cloudFunctions" {
			// 		continue
			// 	}
			// 	// fmt.Printf("Service name:%v\n", service)
			// 	terraformerImportCmd := exec.Command(terraformerPath, "import", "google",
			// 		"--resources="+service,
			// 		"--regions="+region,
			// 		"--projects="+gcpCloudCreds.ProjectID,
			// 		"--path-output=./"+region)
			// 	terraformerImportCmd.Dir = baseOutputDir
			// 	terraformerImportCmd.Env = append(os.Environ(),
			// 		"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
			// 		"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID)

			// 	importOutput, err := terraformerImportCmd.CombinedOutput()
			// 	// fmt.Printf("importOutput\n%v", string(importOutput))
			// 	if err != nil {
			// 		mu.Lock()
			// 		errors = append(errors, fmt.Errorf("error running Terraformer for GCP region %s: %v\nOutput: %s", region, err, string(importOutput)))
			// 		mu.Unlock()
			// 		return
			// 	}
			// }

			if err := mergeFilesOfRefion(baseOutputDir, "google"); err != nil {
				fmt.Printf("Internal error: %v\n", err)
			}

			// if err := removedWorkDir(filepath.Join(baseOutputDir, "all_resources_in_gcp-"+account.ID+".tf"), regionDir, "google"); err != nil {
			// 	fmt.Printf("Internal error: %v\n", err)
			// }
			removedWorkDir(filepath.Join(baseOutputDir, "all_resources_in_gcp-"+account.ID+".tf"), regionDir, "google")

			outputCompletedServiceCount++
			log.Printf("✅ Successfully generated Terraform code for region %s (%v/%v)", region, outputCompletedServiceCount, len(regions))
		}(region, i)
	}

	wg.Wait()

	// Handle errors after all regions are processed
	if len(errors) > 0 {
		return fmt.Errorf("encountered errors during GCP Terraformer process: %v", errors)
	}

	os.RemoveAll(filepath.Join(baseOutputDir, ".terraform"))

	os.Remove(filepath.Join(baseOutputDir, ".terraform.lock.hcl"))

	os.Remove(filepath.Join(baseOutputDir, "main.tf"))

	log.Printf("✅ Completed GCP Terraformer process for account: %s", account.ID)
	return nil
}

// runTerraformerAzure executes Terraformer for Azure to generate resources
func runTerraformerAzure(account CloudAccount) error {
	log.Printf("Starting process for account: %s", account.ID)

	// Process Azure credentials
	log.Println("Processing Azure credentials...")
	azureCreds, ok := account.Credentials.(map[string]interface{})
	if !ok {
		return fmt.Errorf("❌ invalid credentials type for Azure account %s", account.ID)
	}

	// Extract Azure credentials from the map
	subscriptionID, ok := azureCreds["subscription_id"].(string)
	if !ok {
		return fmt.Errorf("❌ invalid or missing subscription_id for Azure account %s", account.ID)
	}

	tenantID, ok := azureCreds["tenant_id"].(string)
	if !ok {
		return fmt.Errorf("❌ invalid or missing tenant_id for Azure account %s", account.ID)
	}

	log.Println("✅ Azure credentials processed successfully")

	// Create base output directory
	baseOutputDir := fmt.Sprintf("generated/azure-%s", account.ID)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	if err := createMainTF("azure", baseOutputDir, ""); err != nil {
		return fmt.Errorf("error writing global main.tf: %v", err)
	}

	// Initialize Terraform
	terraformInitCmd := exec.Command("terraform", "init")
	terraformInitCmd.Dir = baseOutputDir
	initOutput, err := terraformInitCmd.CombinedOutput()
	if err != nil {
		log.Printf("Terraform init output:\n%s", string(initOutput))
		return fmt.Errorf("error running terraform init: %v", err)
	}

	// Get all available Azure services
	resources := getAvailableAzureServices()
	log.Printf("Starting import of all resources across subscription...")

	// Run Terraformer for all resources without specifying resource group
	terraformerImportCmd := exec.Command("terraformer", "import", "azure",
		"--resources="+strings.Join(resources, ","),
		"--path-pattern={output}/{provider}",
		"--path-output=./",
		"--compact")
	terraformerImportCmd.Dir = baseOutputDir
	terraformerImportCmd.Env = append(os.Environ(),
		"ARM_SUBSCRIPTION_ID="+subscriptionID,
		"ARM_TENANT_ID="+tenantID)

	importOutput, err := terraformerImportCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("error running Terraformer: %v\nOutput: %s", err, string(importOutput))
	}

	// Merge all resource files into a single file
	mergedFilePath := filepath.Join(baseOutputDir, fmt.Sprintf("all_resources_in_azure-%s.tf", azureCreds["name"].(string)))
	if err := mergeAzureFiles(filepath.Join(baseOutputDir, "azurerm"), mergedFilePath); err != nil {
		return fmt.Errorf("error merging files: %v", err)
	}

	// Cleanup
	os.RemoveAll(filepath.Join(baseOutputDir, "azurerm"))
	os.RemoveAll(filepath.Join(baseOutputDir, ".terraform"))
	os.Remove(filepath.Join(baseOutputDir, ".terraform.lock.hcl"))
	os.Remove(filepath.Join(baseOutputDir, "main.tf"))

	log.Printf("✅ Completed Azure Terraformer process for account: %s", account.ID)
	return nil
}

// mergeAzureFiles consolidates all Azure resource files into a single file
func mergeAzureFiles(azureDir, outputFile string) error {
	var providerContent strings.Builder
	var resourceContent strings.Builder
	var variableContent strings.Builder
	var outputContent strings.Builder

	// Walk through all directories and files
	err := filepath.Walk(azureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Process only .tf files
		if !strings.HasSuffix(info.Name(), ".tf") {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("error reading file %s: %v", path, err)
		}

		// Determine file type and append content accordingly
		relPath, err := filepath.Rel(azureDir, path)
		if err != nil {
			return err
		}

		serviceName := strings.Split(relPath, string(os.PathSeparator))[0]
		fileName := filepath.Base(path)

		if fileName == "provider.tf" {
			// Only include provider block once
			if providerContent.Len() == 0 {
				providerContent.WriteString("# Provider Configuration\n\n")
				providerContent.Write(content)
				providerContent.WriteString("\n")
			}
		} else if fileName == "variables.tf" {
			variableContent.WriteString(fmt.Sprintf("# Variables for %s\n\n", serviceName))
			variableContent.Write(content)
			variableContent.WriteString("\n")
		} else if fileName == "outputs.tf" {
			outputContent.WriteString(fmt.Sprintf("# Outputs for %s\n\n", serviceName))
			outputContent.Write(content)
			outputContent.WriteString("\n")
		} else if strings.HasSuffix(fileName, ".tf") && fileName != "terraform.tfstate" {
			resourceContent.WriteString(fmt.Sprintf("# Resources for %s\n\n", serviceName))
			resourceContent.Write(content)
			resourceContent.WriteString("\n")
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking directory: %v", err)
	}

	// Combine all content in the desired order
	var finalContent strings.Builder
	finalContent.WriteString("# Terraform configuration generated by Terraformer\n\n")
	finalContent.WriteString(providerContent.String())
	finalContent.WriteString(variableContent.String())
	finalContent.WriteString(resourceContent.String())
	finalContent.WriteString(outputContent.String())

	// Write the merged content to the output file
	if err := os.WriteFile(outputFile, []byte(finalContent.String()), 0644); err != nil {
		return fmt.Errorf("error writing merged file: %v", err)
	}

	return nil
}

func removedWorkDir(workingFile, regionDir, provider string) error {
	workingDir := filepath.Join(regionDir, provider)
	// Remove the work directory
	// if err := os.RemoveAll(workingDir); err != nil {
	// 	return fmt.Errorf("error removing work directory %s: %v", workingDir, err)
	// }
	os.RemoveAll(workingDir)

	// if err := os.Remove(workingFile); err != nil {
	// 	return fmt.Errorf("error removing work file %s: %v", workingFile, err)
	// }
	os.Remove(workingFile)

	return nil
}

// mergeFiles consolidates all `.tf` files in the specified directory into a single output file.
func mergeFiles(regionDir, outputFileName string) error {
	var providerContent strings.Builder
	var outputContent strings.Builder
	var resourceContent strings.Builder
	var providerWritten, outputWritten bool // Flags to ensure single inclusion of provider and output sections

	// Walk through all `.tf` files in the directory and its subdirectories
	err := filepath.Walk(regionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error accessing path %s: %w", path, err)
		}

		// Skip directories and the output file itself
		if info.IsDir() || filepath.Base(path) == outputFileName {
			return nil
		}

		// Skip files matching the `all_resources_in_*` pattern
		if strings.HasPrefix(filepath.Base(path), "all_resources_in_") {
			return nil
		}

		// Process only `.tf` files
		if strings.HasSuffix(path, ".tf") {
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", path, err)
			}

			switch filepath.Base(path) {
			case "provider.tf":
				// Add provider.tf content if not already included
				if !providerWritten {
					providerContent.Write(content)
					providerContent.WriteString("\n") // Add spacing
					providerWritten = true
				}
			case "output.tf":
				// Add output.tf content if not already included
				if !outputWritten {
					outputContent.Write(content)
					outputContent.WriteString("\n") // Add spacing
					outputWritten = true
				}
			default:
				// Add all other `.tf` files to resourceContent
				resourceContent.WriteString(fmt.Sprintf("# Start of %s\n\n", filepath.Base(path)))
				resourceContent.Write(content)
				resourceContent.WriteString(fmt.Sprintf("\n# End of %s\n\n", filepath.Base(path)))
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	// Combine all content into the final file
	var mergedContent strings.Builder
	if providerContent.Len() > 0 {
		mergedContent.WriteString("# Provider Definitions\n\n")
		mergedContent.WriteString(providerContent.String())
	}
	if outputContent.Len() > 0 {
		mergedContent.WriteString("# Output Definitions\n\n")
		mergedContent.WriteString(outputContent.String())
	}
	if resourceContent.Len() > 0 {
		mergedContent.WriteString("# Resource Definitions\n\n")
		mergedContent.WriteString(resourceContent.String())
	}

	// Write the merged content to the specified output file
	outputFilePath := filepath.Join(regionDir, outputFileName)
	err = os.WriteFile(outputFilePath, []byte(mergedContent.String()), 0644)
	if err != nil {
		return fmt.Errorf("failed to write merged file to %s: %w", outputFilePath, err)
	}

	// fmt.Printf("Successfully merged files into %s\n", outputFilePath)
	return nil
}

// processRegions consolidates Terraform files in each region directory into a single file.
func mergeFilesOfRefion(baseDir, provider string) error {
	// Walk through all directories within the base directory
	err := filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error accessing path %s: %w", path, err)
		}

		// Process only region directories with `<region>/google/<project-id>` structure
		if info.IsDir() && strings.Contains(path, provider) && !strings.Contains(path, ".terraform") {
			projectDir := filepath.Dir(path) // Get project directory
			regionDir := filepath.Dir(projectDir)
			region := filepath.Base(regionDir)
			outputFileName := fmt.Sprintf("all_resources_in_%s.tf", region)

			err = mergeFiles(regionDir, outputFileName)
			if err != nil {
				return fmt.Errorf("error merging files in region directory %s: %w", regionDir, err)
			}
		}
		return nil
	})
	return err
}

// createMainTF creates the main.tf file for a cloud provider
func createMainTF(provider, dir, fileAttributes string) error {
	var mainTFContent string

	switch provider {
	case "aws":
		mainTFContent = fmt.Sprintf(`
provider "aws" {
  region = "%s"
}
`, fileAttributes)
	case "gcp":
		mainTFContent = fmt.Sprintf(`
terraform {
	required_providers {
		google-beta = {
			source  = "hashicorp/google"
			version = "4.0.0"
		}
	}
	required_version = ">= 0.13"
}

provider "google-beta" {
	project = "%s"
}
`, fileAttributes)
	case "azure":
		mainTFContent = fmt.Sprintf(`
terraform {
required_providers {
	azurerm = {
		source  = "hashicorp/azurerm"
		version = "~> 3.0"
	}
}
}

provider "azurerm" {
features {}
skip_provider_registration = true
}
`)
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	err := os.WriteFile(fmt.Sprintf("%s/main.tf", dir), []byte(mainTFContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing main.tf: %v", err)
	}

	return nil
}

// getAWSRegions is a wrapper function that tries different methods to get regions
func getAWSRegions() []string {
	return []string{
		"us-east-1",
		"us-east-2",
		"us-west-1",
		"us-west-2",
		"af-south-1",
		"ap-east-1",
		"ap-south-2",
		"ap-southeast-3",
		"ap-southeast-5",
		"ap-southeast-4",
		"ap-south-1",
		"ap-northeast-3",
		"ap-northeast-2",
		"ap-southeast-1",
		"ap-southeast-2",
		"ap-northeast-1",
		"ca-central-1",
		"ca-west-1",
		"cn-north-1",
		"cn-northwest-1",
		"eu-central-1",
		"eu-west-1",
		"eu-west-2",
		"eu-south-1",
		"eu-west-3",
		"eu-south-2",
		"eu-north-1",
		"eu-central-2",
		"il-central-1",
		"me-south-1",
		"me-central-1",
		"sa-east-1",
	}
}

// getAvailableServicesForRegion returns a list of services available in the specified region
func getAvailableAWSServices() []string {
	return []string{
		"accessanalyzer",
		"acm",
		"alb",
		"api_gateway",
		"appsync",
		"auto_scaling",
		"batch",
		"budgets",
		"cloud9",
		"cloudformation",
		"cloudfront",
		"cloudhsm",
		"cloudtrail",
		"cloudwatch",
		"codebuild",
		"codecommit",
		"codedeploy",
		"codepipeline",
		"cognito",
		"config",
		"customer_gateway",
		"datapipeline",
		"devicefarm",
		"docdb",
		"dynamodb",
		"ebs",
		"ec2_instance",
		"ecr",
		"ecrpublic",
		"ecs",
		"efs",
		"eip",
		"eks",
		"elastic_beanstalk",
		"elasticache",
		"elb",
		"emr",
		"eni",
		"es",
		"firehose",
		"glue",
		"iam",
		"identitystore",
		"igw",
		"iot",
		"kinesis",
		"kms",
		"lambda",
		"logs",
		"media_package",
		"media_store",
		"medialive",
		"msk",
		"nacl",
		"nat",
		"opsworks",
		"organization",
		"qldb",
		"rds",
		"redshift",
		"resourcegroups",
		"route53",
		"route_table",
		"s3",
		"secretsmanager",
		"securityhub",
		"servicecatalog",
		"ses",
		"sfn",
		"sg",
		"sns",
		"sqs",
		"ssm",
		"subnet",
		"swf",
		"transit_gateway",
		"vpc",
		"vpc_peering",
		"vpn_connection",
		"vpn_gateway",
		"waf",
		"waf_regional",
		"wafv2_cloudfront",
		"wafv2_regional",
		"workspaces",
		"xray",
	}
}

func getGCPRegions(output []byte) []string {
	var regions []string
	scanner := bufio.NewScanner(bytes.NewReader(output))

	// skip header
	scanner.Scan()

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) > 0 {
			regions = append(regions, fields[0])
		}
	}

	return regions
}

// escapeNewlines escapes newlines in the private key so that it can be inserted into a JSON string
func escapeNewlines(input string) string {
	return strings.ReplaceAll(input, "\n", "\\n")
}

// Asset represents the structure of a GCP asset
type Asset struct {
	AssetType string `json:"assetType"`
	Name      string `json:"name"`
}

// getResourceTypeMapping returns a map of GCP asset types to Terraformer resource types
func getResourceTypeMapping() map[string]string {
	return map[string]string{
		// Compute Engine
		"compute.googleapis.com/Address":                    "addresses",
		"compute.googleapis.com/GlobalAddress":              "globalAddresses",
		"compute.googleapis.com/Autoscaler":                 "autoscalers",
		"compute.googleapis.com/RegionAutoscaler":           "regionAutoscalers",
		"compute.googleapis.com/BackendBucket":              "backendBuckets",
		"compute.googleapis.com/BackendService":             "backendServices",
		"compute.googleapis.com/RegionBackendService":       "regionBackendServices",
		"compute.googleapis.com/Disk":                       "disks",
		"compute.googleapis.com/RegionDisk":                 "regionDisks",
		"compute.googleapis.com/Firewall":                   "firewall",
		"compute.googleapis.com/ForwardingRule":             "forwardingRules",
		"compute.googleapis.com/GlobalForwardingRule":       "globalForwardingRules",
		"compute.googleapis.com/HealthCheck":                "healthChecks",
		"compute.googleapis.com/RegionHealthCheck":          "regionHealthChecks",
		"compute.googleapis.com/HttpHealthCheck":            "httpHealthChecks",
		"compute.googleapis.com/HttpsHealthCheck":           "httpsHealthChecks",
		"compute.googleapis.com/Image":                      "images",
		"compute.googleapis.com/Instance":                   "instances",
		"compute.googleapis.com/InstanceGroup":              "instanceGroups",
		"compute.googleapis.com/RegionInstanceGroup":        "regionInstanceGroups",
		"compute.googleapis.com/InstanceGroupManager":       "instanceGroupManagers",
		"compute.googleapis.com/RegionInstanceGroupManager": "regionInstanceGroupManagers",
		"compute.googleapis.com/InstanceTemplate":           "instanceTemplates",
		"compute.googleapis.com/Network":                    "networks",
		"compute.googleapis.com/NetworkEndpointGroup":       "networkEndpointGroups",
		"compute.googleapis.com/NodeGroup":                  "nodeGroups",
		"compute.googleapis.com/NodeTemplate":               "nodeTemplates",
		"compute.googleapis.com/PacketMirroring":            "packetMirrorings",
		"compute.googleapis.com/Reservation":                "reservations",
		"compute.googleapis.com/ResourcePolicy":             "resourcePolicies",
		"compute.googleapis.com/Route":                      "routes",
		"compute.googleapis.com/Router":                     "routers",
		"compute.googleapis.com/SecurityPolicy":             "securityPolicies",
		"compute.googleapis.com/SslCertificate":             "sslCertificates",
		"compute.googleapis.com/RegionSslCertificate":       "regionSslCertificates",
		"compute.googleapis.com/SslPolicy":                  "sslPolicies",
		"compute.googleapis.com/Subnetwork":                 "subnetworks",
		"compute.googleapis.com/TargetHttpProxy":            "targetHttpProxies",
		"compute.googleapis.com/RegionTargetHttpProxy":      "regionTargetHttpProxies",
		"compute.googleapis.com/TargetHttpsProxy":           "targetHttpsProxies",
		"compute.googleapis.com/RegionTargetHttpsProxy":     "regionTargetHttpsProxies",
		"compute.googleapis.com/TargetInstance":             "targetInstances",
		"compute.googleapis.com/TargetPool":                 "targetPools",
		"compute.googleapis.com/TargetSslProxy":             "targetSslProxies",
		"compute.googleapis.com/TargetTcpProxy":             "targetTcpProxies",
		"compute.googleapis.com/TargetVpnGateway":           "targetVpnGateways",
		"compute.googleapis.com/UrlMap":                     "urlMaps",
		"compute.googleapis.com/RegionUrlMap":               "regionUrlMaps",
		"compute.googleapis.com/VpnTunnel":                  "vpnTunnels",
		"compute.googleapis.com/ExternalVpnGateway":         "externalVpnGateways",
		"compute.googleapis.com/InterconnectAttachment":     "interconnectAttachments",

		// Cloud Storage
		"storage.googleapis.com/Bucket": "gcs",

		// BigQuery
		"bigquery.googleapis.com/Dataset": "bigQuery",
		"bigquery.googleapis.com/Table":   "bigQuery",

		// Cloud Functions
		"cloudfunctions.googleapis.com/Function": "cloudFunctions",

		// Cloud Build
		"cloudbuild.googleapis.com/Trigger": "cloudbuild",

		// Cloud SQL
		"sql.googleapis.com/Instance": "cloudsql",

		// Cloud Tasks
		"cloudtasks.googleapis.com/Queue": "cloudtasks",

		// Dataproc
		"dataproc.googleapis.com/Cluster": "dataProc",

		// GKE
		"container.googleapis.com/Cluster": "gke",

		// IAM
		"iam.googleapis.com/ServiceAccount": "iam",
		"iam.googleapis.com/Role":           "iam",

		// Cloud DNS
		"dns.googleapis.com/ManagedZone": "dns",

		// Cloud KMS
		"cloudkms.googleapis.com/KeyRing":   "kms",
		"cloudkms.googleapis.com/CryptoKey": "kms",

		// Cloud Logging
		"logging.googleapis.com/LogMetric": "logging",
		"logging.googleapis.com/LogSink":   "logging",
		"logging.googleapis.com/LogBucket": "logging",

		// Memorystore
		"redis.googleapis.com/Instance": "memoryStore",

		// Cloud Monitoring
		"monitoring.googleapis.com/AlertPolicy":         "monitoring",
		"monitoring.googleapis.com/NotificationChannel": "monitoring",

		// Cloud Pub/Sub
		"pubsub.googleapis.com/Topic":        "pubsub",
		"pubsub.googleapis.com/Subscription": "pubsub",

		// Project
		"cloudresourcemanager.googleapis.com/Project": "project",

		// Cloud Scheduler
		"cloudscheduler.googleapis.com/Job": "schedulerJobs",
	}
}

// convertToTerraformerResource converts GCP asset type to Terraformer resource type
func convertToTerraformerResource(assetType string) string {
	resourceTypeMapping := getResourceTypeMapping()
	if resource, ok := resourceTypeMapping[assetType]; ok {
		return resource
	}
	return ""
}

// getAzureRegions returns a list of Azure regions
func getAzureRegions() []string {
	return []string{
		"eastasia",
		"southeastasia",
		"centralus",
		"eastus",
		"eastus2",
		"westus",
		"westus2",
		"westus3",
		"northcentralus",
		"southcentralus",
		"northeurope",
		"westeurope",
		"japanwest",
		"japaneast",
		"brazilsouth",
		"australiaeast",
		"australiasoutheast",
		"southindia",
		"centralindia",
		"westindia",
		"canadacentral",
		"canadaeast",
		"uksouth",
		"ukwest",
		"koreacentral",
		"koreasouth",
		"francecentral",
		"francesouth",
		"australiacentral",
		"australiacentral2",
		"uaenorth",
		"uaecentral",
		"switzerlandnorth",
		"switzerlandwest",
		"germanynorth",
		"germanywestcentral",
		"norwaywest",
		"norwayeast",
		"brazilsoutheast",
		"westcentralus",
	}
}

// getAvailableAzureServices returns a list of available Azure services for Terraformer
func getAvailableAzureServices() []string {
	return []string{
		"analysis",
		"app_service",
		"application_gateway",
		"container",
		"cosmosdb",
		"data_factory",
		"database",
		"databricks",
		"disk",
		"dns",
		"eventhub",
		"keyvault",
		"load_balancer",
		"management_lock",
		"network_interface",
		"network_security_group",
		"network_watcher",
		"private_dns",
		"private_endpoint",
		"public_ip",
		"purview",
		"redis",
		"resource_group",
		"route_table",
		"scaleset",
		"security_center_contact",
		"security_center_subscription_pricing",
		"ssh_public_key",
		"storage_account",
		"storage_blob",
		"storage_container",
		"subnet",
		"synapse",
		"virtual_machine",
		"virtual_network",
	}
}
