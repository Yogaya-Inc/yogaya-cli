/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

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
	RenameDirWithBackup(baseOutputDir)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	if err := createMainTF("azure", baseOutputDir, []string{""}); err != nil {
		return fmt.Errorf("error writing global main.tf: %v", err)
	}

	// Initialize Terraform
	terraformInitCmd := exec.Command("terraform", "init", "--upgrade")
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
		// "--path-pattern={output}/{provider}",
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
	var providerWritten bool // Flags to ensure single inclusion of provider sections

	// Walk through all directories and files
	err := filepath.Walk(azureDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories and non-.tf files
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".tf") {
			return nil
		}

		// Read file content
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("error reading file %s: %v", path, err)
		}

		if len(string(content)) == 0 || string(content) == "\n" {
			return nil
		}

		fileName := filepath.Base(path)
		switch fileName {
		case "provider.tf":
			if !providerWritten {
				providerContent.WriteString("# Provider Configuration\n\n")
				providerContent.Write(content)
				providerContent.WriteString("\n")
				providerWritten = true
			}
		case "variables.tf":
			variableContent.WriteString("# Variable Definitions\n\n")
			variableContent.Write(content)
			variableContent.WriteString("\n")
		case "outputs.tf":
			outputContent.WriteString("# Output Definitions\n\n")
			outputContent.Write(content)
			outputContent.WriteString("\n")
		case "resources.tf":
			resourceContent.WriteString("# Resource Definitions\n\n")
			resourceContent.Write(content)
			resourceContent.WriteString("\n")
		default:
			if fileName != "terraform.tfstate" {
				resourceContent.WriteString(fmt.Sprintf("# Additional Resources from %s\n\n", path))
				resourceContent.Write(content)
				resourceContent.WriteString("\n")
			}
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
