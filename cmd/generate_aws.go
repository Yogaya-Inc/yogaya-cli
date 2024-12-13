/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go/aws"
)

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
	RenameDirWithBackup(baseOutputDir)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	// Get AWS regions
	regions := getAWSRegions()
	// regions := []string{"ap-northeast-1"} // for debug
	// log.Printf("Processing %d AWS regions: %v\n", len(regions), regions)

	// Define maximum number of concurrent workers
	maxConcurrency := 7 // Max Threads
	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex // To protect shared resources like log output
	errors := []error{}

	outputCompletedServiceCount := 0

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

			if err := createMainTF("aws", regionDir, []string{region}); err != nil {
				fmt.Printf("error writing global main.tf: %v", err)
				return
			}

			// log.Printf("Running terraform init in %s", regionDir)
			terraformInitCmd := exec.Command("terraform", "init", "--upgrade")
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

			terraformerImportCmd := exec.Command("terraformer", "import", "aws",
				"--resources=*",
				"--regions="+region,
				"--path-output=./",
				"--compact")
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
			// fmt.Printf("importOutput\n%v", string(importOutput))

			mergeFilesOfRefion(regionDir, "aws")

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

	log.Printf("✅ Completed AWS Terraformer process for account: %s", account.ID)
	return nil
}

func getAWSRegions() []string {

	regions := []string{}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return getAWSRegionsHardCoded()
	}

	// Create EC2 client
	client := ec2.NewFromConfig(cfg)

	// Describe regions
	resp, err := client.DescribeRegions(context.TODO(), &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(true),
	})

	if err != nil {
		return getAWSRegionsHardCoded()
	}

	for _, region := range resp.Regions {
		regions = append(regions, *region.RegionName)
	}
	return regions
}

func getAWSRegionsHardCoded() []string {
	return []string{
		"ap-south-2",
		"ap-south-1",
		"eu-south-1",
		"eu-south-2",
		"me-central-1",
		"il-central-1",
		"ca-central-1",
		"eu-central-1",
		"eu-central-2",
		"us-west-1",
		"us-west-2",
		"af-south-1",
		"eu-north-1",
		"eu-west-3",
		"eu-west-2",
		"eu-west-1",
		"ap-northeast-3",
		"ap-northeast-2",
		"me-south-1",
		"ap-northeast-1",
		"sa-east-1",
		"ap-east-1",
		"ca-west-1",
		"ap-southeast-1",
		"ap-southeast-2",
		"ap-southeast-3",
		"ap-southeast-4",
		"us-east-1",
		"ap-southeast-5",
		"us-east-2",
	}
}
