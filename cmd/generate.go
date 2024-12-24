/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type ChangeType string

const (
	Added    ChangeType = "A"
	Modified ChangeType = "M"
	Deleted  ChangeType = "D"
)

// ResourceAnalyzer handles the analysis of cloud resources
type ResourceAnalyzer struct {
	BaseDir   string
	Changes   map[string]map[string]map[string]map[string]ChangeType // account -> provider -> region -> service -> change type
	Providers map[string]*CloudProvider
}

type CloudProvider struct {
	Name    string
	Regions map[string]*Region
}

type Region struct {
	Name     string
	Services map[string]bool
}

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

	// Load the credentials file
	cm, err := NewCredentialManager()
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

		var err error
		switch account.Provider {
		case "aws":
			err = runTerraformerAWS(account)
		case "gcp":
			err = runTerraformerGCP(account)
		case "azure":
			err = runTerraformerAzure(account)
		default:
			log.Printf("⚠️ Skipping unsupported provider: %s", account.Provider)
			continue
		}

		if err != nil {
			errFlag = true
			log.Printf("❌ Error generating Terraform code for %s account %s: %v",
				account.Provider, account.ID, err)
		} else {
			log.Printf("✅ Successfully generated Terraform code for %s account %s",
				account.Provider, account.ID)
		}
	}

	log.Println("Analyzing resource changes...")

	analyzer := &ResourceAnalyzer{
		BaseDir: "generated",
		Changes: make(map[string]map[string]map[string]map[string]ChangeType),
	}

	if err := analyzer.AnalyzeResourceChanges(); err != nil {
		log.Printf("❌ Error analyzing resource changes: %v", err)
		errFlag = false
	}

	if !errFlag {
		log.Println("Generation process completed")
	}
}

func (ra *ResourceAnalyzer) AnalyzeResourceChanges() error {
	ra.Changes = make(map[string]map[string]map[string]map[string]ChangeType)

	// Run git config command
	gitConfigCmd := exec.Command("git", "config", "core.quotepath", "false")
	_, err := gitConfigCmd.Output()
	if err != nil {
		return fmt.Errorf("error running git config: %v", err)
	}

	// Run git add command
	gitAddCmd := exec.Command("git", "add", "./generated")
	_, err = gitAddCmd.Output()
	if err != nil {
		return fmt.Errorf("error running git add: %v", err)
	}

	// Run git diff command
	gitDiffcmd := exec.Command("git", "diff", "--cached", "--name-status")
	gitDiffcmdOutput, err := gitDiffcmd.Output()
	if err != nil {
		return fmt.Errorf("error running git diff: %v", err)
	}

	// Process each changed file
	scanner := bufio.NewScanner(strings.NewReader(string(gitDiffcmdOutput)))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		changeType := parts[0]
		filePath := parts[1]

		if !strings.HasSuffix(filePath, ".tf") {
			continue
		}

		// Extract account, provider, and region from path
		pathParts := strings.Split(filePath, string(os.PathSeparator))
		if len(pathParts) < 3 { // Need at least: generated/account
			continue
		}

		// Parse path components
		accountDir := pathParts[1]                        // e.g., aws-5079e9219e2e
		providerName := strings.Split(accountDir, "-")[0] // aws-xxxx -> aws
		// region := pathParts[2]
		var region string
		switch providerName {
		case "azure":
			region = "global"
		case "aws", "gcp":
			if len(pathParts) < 4 { // AWS/GCP needs: generated/account/provider/region
				continue
			}
			region = pathParts[2]
		default:
			continue
		}

		// Initialize nested maps if needed
		if _, exists := ra.Changes[accountDir]; !exists {
			ra.Changes[accountDir] = make(map[string]map[string]map[string]ChangeType)
		}
		if _, exists := ra.Changes[accountDir][providerName]; !exists {
			ra.Changes[accountDir][providerName] = make(map[string]map[string]ChangeType)
		}
		if _, exists := ra.Changes[accountDir][providerName][region]; !exists {
			ra.Changes[accountDir][providerName][region] = make(map[string]ChangeType)
		}

		// Extract services from current and previous versions
		currentServices := ra.extractServicesFromFile(filePath, changeType != "D")
		var previousServices []string
		if changeType != "A" {
			prevCmd := exec.Command("git", "show", "HEAD:"+filePath)
			prevOutput, err := prevCmd.Output()
			if err == nil {
				previousServices = ra.extractServicesFromContent(string(prevOutput))
			}
		}

		// Process changes
		ra.processServiceChanges(
			ra.Changes[accountDir][providerName][region],
			currentServices,
			previousServices,
			ChangeType(changeType),
		)
	}

	ra.CommitChanges()
	return nil
}

func (ra *ResourceAnalyzer) extractServicesFromFile(path string, exists bool) []string {
	if !exists {
		return nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	return ra.extractServicesFromContent(string(content))
}

func (ra *ResourceAnalyzer) extractServicesFromContent(content string) []string {
	services := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(content))

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "resource") {
			parts := strings.Split(line, "\"")
			if len(parts) > 1 {
				resourceType := parts[1]
				serviceParts := strings.Split(resourceType, "_")
				if len(serviceParts) > 1 {
					switch serviceParts[0] {
					case "azurerm":
						if len(serviceParts) > 2 {
							services[serviceParts[1]] = true
						}
					case "aws", "google":
						services[serviceParts[1]] = true
					}
				}
			}
		}
	}

	result := make([]string, 0, len(services))
	for service := range services {
		result = append(result, service)
	}
	sort.Strings(result)
	return result
}

func (ra *ResourceAnalyzer) processServiceChanges(
	regionChanges map[string]ChangeType,
	currentServices []string,
	previousServices []string,
	fileChangeType ChangeType) {

	// Convert slices to maps for easier comparison
	currentMap := make(map[string]bool)
	for _, service := range currentServices {
		currentMap[service] = true
	}

	previousMap := make(map[string]bool)
	for _, service := range previousServices {
		previousMap[service] = true
	}

	// Process based on file change type
	switch fileChangeType {
	case Added:
		for service := range currentMap {
			regionChanges[service] = Added
		}
	case Deleted:
		for service := range previousMap {
			regionChanges[service] = Deleted
		}
	case Modified:
		// Check for added services
		for service := range currentMap {
			if !previousMap[service] {
				regionChanges[service] = Added
			} else {
				regionChanges[service] = Modified
			}
		}
		// Check for deleted services
		for service := range previousMap {
			if !currentMap[service] {
				regionChanges[service] = Deleted
			}
		}
	}
}

func (ra *ResourceAnalyzer) CommitChanges() string {
	var message strings.Builder
	message.WriteString("Resource Changes Summary:\n")

	for accountDir, providers := range ra.Changes {
		message.WriteString("\n")
		message.WriteString(strings.Repeat("=", 70) + "\n")
		message.WriteString(fmt.Sprintf("\nAccount: %s\n", accountDir))

		for provider, regions := range providers {
			// fmt.Println(strings.Repeat("-", 50))
			message.WriteString(fmt.Sprintf("\n%s Resources:\n", strings.ToUpper(provider)))

			for region, services := range regions {
				if len(services) > 0 {
					message.WriteString(fmt.Sprintf("\n  Region: %s\n", region))

					// Group changes by type
					added := make([]string, 0)
					modified := make([]string, 0)
					deleted := make([]string, 0)

					for service, changeType := range services {
						switch changeType {
						case Added:
							added = append(added, service)
						case Modified:
							modified = append(modified, service)
						case Deleted:
							deleted = append(deleted, service)
						}
					}

					// Sort services for consistent output
					sort.Strings(added)
					sort.Strings(modified)
					sort.Strings(deleted)

					if len(added) > 0 {
						message.WriteString("    Added Services:\n")
						for _, service := range added {
							message.WriteString(fmt.Sprintf("      + %s\n", service))
						}
					}

					if len(modified) > 0 {
						message.WriteString("    Modified Services:\n")
						for _, service := range modified {
							message.WriteString(fmt.Sprintf("      ~ %s\n", service))
						}
					}

					if len(deleted) > 0 {
						message.WriteString("    Deleted Services:\n")
						for _, service := range deleted {
							message.WriteString(fmt.Sprintf("      - %s\n", service))
						}
					}
				}
			}
		}
	}

	commitMsg := message.String()
	// fmt.Print(commitMsg)

	// Run git cmmit command
	cmd := exec.Command("git", "commit", "-m", commitMsg)
	if err := cmd.Run(); err != nil {
		log.Printf("❌ Error creating git commit: %v", err)
		return ""
	}

	return commitMsg
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
		mainTFContent = `
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
`
	default:
		return fmt.Errorf("unsupported provider: %s", provider)
	}

	err := os.WriteFile(fmt.Sprintf("%s/main.tf", dir), []byte(mainTFContent), 0644)
	if err != nil {
		return fmt.Errorf("error writing main.tf: %v", err)
	}

	return nil
}
