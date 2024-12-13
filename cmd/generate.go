/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"fmt"
	"log"
	"os"
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

	homeDir, err := os.UserHomeDir()
	pluginDir := filepath.Join(homeDir, ".terraform.d", "plugins", "darwin_arm64")
	// Create directory with all parent directories if they don't exist
	err = os.MkdirAll(pluginDir, 0755)

	errFlag := false

	// Iterate over each cloud account and run Terraformer
	for i, account := range cm.config.Accounts {
		if i > 0 {
			log.Println("------------------------------------------------------------")
		}
		log.Printf("Processing account %d/%d: %s (%s)", i+1, len(cm.config.Accounts), account.ID, account.Provider)

		switch account.Provider {
		case "aws":
			if err := runTerraformerAWS(account); err != nil {
				errFlag = true
				log.Printf("❌ Error generating Terraform code for AWS account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for AWS account %s", account.ID)
			}
		case "gcp":
			if err := runTerraformerGCP(account); err != nil {
				errFlag = true
				log.Printf("❌ Error generating Terraform code for GCP account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for GCP account %s", account.ID)
			}
		case "azure":
			if err := runTerraformerAzure(account); err != nil {
				errFlag = true
				log.Printf("❌ Error generating Terraform code for Azure account %s: %v", account.ID, err)
			} else {
				log.Printf("✅ Successfully generated Terraform code for Azure account %s", account.ID)
			}
		default:
			log.Printf("⚠️ Skipping unsupported provider: %s", account.Provider)
		}
	}
	if !errFlag {
		log.Println("Generation process completed")
	}
}

// RenameDirWithBackup renames a directory by adding "_bk" suffix if it already exists
func RenameDirWithBackup(dirPath string) error {
	// Check if directory exists
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil
	}

	// Generate new path name
	backupPath := dirPath + "_bk"

	// Add number suffix if backup directory already exists
	counter := 1
	for {
		_, err := os.Stat(backupPath)
		if os.IsNotExist(err) {
			break
		}
		backupPath = fmt.Sprintf("%s_bk%d", dirPath, counter)
		counter++
	}

	// Execute rename operation
	if err := os.Rename(dirPath, backupPath); err != nil {
		return fmt.Errorf("failed to rename %v directory: %v", dirPath, err)
	}
	log.Printf("✅ %v move to %v\n", dirPath, backupPath)
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
	var providerWritten bool // Flags to ensure single inclusion of provider sections
	var mainWritten bool     // Flags to ensure single inclusion of provider sections
	// var outputWritten bool   // Flags to ensure single inclusion of output sections

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

			if len(string(content)) == 0 || string(content) == "\n" {
				return nil
			}

			switch filepath.Base(path) {
			case "provider.tf":
				// Add provider.tf content if not already included
				if !providerWritten {
					providerContent.Write(content)
					providerContent.WriteString("\n") // Add spacing
					providerWritten = true
				}
			case "main.tf":
				if !mainWritten {
					mainWritten = true
				} else {
					return nil
				}
			// case "output.tf":
			// 	// Add output.tf content if not already included
			// 	if !outputWritten {
			// 		outputContent.Write(content)
			// 		outputContent.WriteString("\n") // Add spacing
			// 		outputWritten = true
			// 	}
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
func createMainTF(provider, dir string, fileAttributes []string) error {
	var mainTFContent string

	switch provider {
	case "aws":
		mainTFContent = fmt.Sprintf(`
terraform {
  required_providers {
    aws = {}
  }
  required_version = ">= 0.13"
}

provider "aws" {
  region = "%s"
}
`, fileAttributes[0])
	case "gcp":
		mainTFContent = fmt.Sprintf(`
terraform {
  required_providers {
    google = {
      source = "hashicorp/google"
    }
  }
  required_version = ">= 0.13"
}

provider "google" {
  project = "%s"
  region = "%s"
}
`, fileAttributes[0], fileAttributes[1])
	case "azure":
		mainTFContent = fmt.Sprintf(`
terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 3.0.0, < 4.0.0"
    }
  }
}

provider "azurerm" {
  features {}
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
