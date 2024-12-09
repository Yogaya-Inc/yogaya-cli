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
	"sync"
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
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
	}

	outputCompletedServiceCount := 0

	// Get AWS regions
	regions := getAWSRegions()
	// regions = []string{"ap-northeast-1"} // for debug
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
			if len(resources) == 0 {
				log.Printf("⚠️ No services configured for region %s, skipping", region)
				return
			}
			terraformerImportCmd := exec.Command("terraformer", "import", "aws",
				// "--resources="+strings.Join(resources, ","),
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
	os.Remove("./all_resources_in_..tf")

	log.Printf("✅ Completed AWS Terraformer process for account: %s", account.ID)
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
