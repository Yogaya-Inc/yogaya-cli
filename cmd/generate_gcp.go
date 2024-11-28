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
