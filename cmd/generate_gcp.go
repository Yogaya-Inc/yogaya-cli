package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
)

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
	RenameDirWithBackup(baseOutputDir)
	if err := os.MkdirAll(baseOutputDir, 0755); err != nil {
		return fmt.Errorf("error creating base output directory: %v", err)
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

	regions := getGCPRegions(gcpCloudCreds.ProjectID)
	// regions := []string{"asia-southeast2", "africa-south1"} // for debug
	// log.Printf("Processing %d GCP regions: %v", len(regions), regions)

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

			if err := createMainTF("gcp", regionDir, []string{gcpCloudCreds.ProjectID, region}); err != nil {
				fmt.Printf("error writing global main.tf: %v", err)
				return
			}

			// Initialize Terraform in base directory
			terraformInitCmd := exec.Command("terraform", "init", "-upgrade")
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

			terraformerImportCmd := exec.Command("terraformer", "import", "google",
				"--resources=*",
				"--regions="+region,
				"--projects="+gcpCloudCreds.ProjectID,
				"--path-output=./",
				"--compact")
			terraformerImportCmd.Dir = regionDir
			terraformerImportCmd.Env = append(os.Environ(),
				"GOOGLE_APPLICATION_CREDENTIALS="+tempFile.Name(),
				"GOOGLE_CLOUD_PROJECT="+gcpCloudCreds.ProjectID)

			importOutput, err := terraformerImportCmd.CombinedOutput()
			if err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("error running Terraformer for GCP region %s: %v\nOutput: %s", region, err, string(importOutput)))
				mu.Unlock()
				return
			}
			// fmt.Printf("importOutput\n%v", string(importOutput))

			if err := mergeFilesOfRefion(regionDir, "google"); err != nil {
				fmt.Printf("Internal error: %v\n", err)
			}

			removedWorkDir(filepath.Join(baseOutputDir, "all_resources_in_gcp-"+account.ID+".tf"), regionDir, "google")

			os.RemoveAll(filepath.Join(regionDir, ".terraform"))

			os.Remove(filepath.Join(regionDir, ".terraform.lock.hcl"))

			os.Remove(filepath.Join(regionDir, "main.tf"))

			outputCompletedServiceCount++
			log.Printf("✅ Successfully generated Terraform code for region %s (%v/%v)", region, outputCompletedServiceCount, len(regions))
		}(region, i)
	}

	wg.Wait()

	// Handle errors after all regions are processed
	if len(errors) > 0 {
		return fmt.Errorf("encountered errors during GCP Terraformer process: %v", errors)
	}

	log.Printf("✅ Completed GCP Terraformer process for account: %s", account.ID)
	return nil
}

func getGCPRegions(projectID string) []string {
	regions := []string{}

	// Create a context
	ctx := context.Background()

	// Create a client for the Compute Engine API
	client, err := compute.NewRegionsRESTClient(ctx)
	if err != nil {
		return getGCPRegionsHardCoded()
	}
	defer client.Close()

	// List regions
	req := &computepb.ListRegionsRequest{
		Project: projectID,
	}

	it := client.List(ctx, req)

	for {
		region, err := it.Next()
		if err != nil {
			if err.Error() == "no more items in iterator" {
				break
			}
			return getGCPRegionsHardCoded()
		}
		regions = append(regions, region.GetName())
	}
	return regions
}

func getGCPRegionsHardCoded() []string {
	return []string{
		"africa-south1",
		"asia-east1",
		"asia-east2",
		"asia-northeast1",
		"asia-northeast2",
		"asia-northeast3",
		"asia-south1",
		"asia-south2",
		"asia-southeast1",
		"asia-southeast2",
		"australia-southeast1",
		"australia-southeast2",
		"europe-central2",
		"europe-north1",
		"europe-southwest1",
		"europe-west1",
		"europe-west10",
		"europe-west12",
		"europe-west2",
		"europe-west3",
		"europe-west4",
		"europe-west6",
		"europe-west8",
		"europe-west9",
		"me-central1",
		"me-central2",
		"me-west1",
		"northamerica-northeast1",
		"northamerica-northeast2",
		"northamerica-south1",
		"southamerica-east1",
		"southamerica-west1",
		"us-central1",
		"us-east1",
		"us-east4",
		"us-east5",
		"us-south1",
		"us-west1",
		"us-west2",
		"us-west3",
		"us-west4",
	}
}

// escapeNewlines escapes newlines in the private key so that it can be inserted into a JSON string
func escapeNewlines(input string) string {
	return strings.ReplaceAll(input, "\n", "\\n")
}
