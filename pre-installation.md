# Pre-Installation Steps

## Requirements

Before installing and using the Yogaya CLI, ensure the following tools are pre-installed:

- **Go** 1.23.3 darwin/arm64 or later
- **Git**: 2.39.3 (Apple Git-146)
- **Terraform**: 1.9.8　(latest as of 11/19/2024)
- **Terraformer**: 0.8.24　(latest as of 11/19/2024)
- **AWS CLI**: 2.19.1 Python/3.12.7 Darwin/23.6.0 source/arm64(latest as of 11/19/2024)
- **Google Cloud SDK**: 501.0.0　(latest as of 11/19/2024)

## Installation

- **Git** (required version: 2.39.3 (Apple Git-146))

## Installing Terraform

  ```bash
  brew install terraform
  ```

  OR

  ```bash
  brew install tfenv
  tfenv install 1.9.8
  tfenv use 1.9.8
  ```

## Installing Terraformer

  ```bash
  brew install terraformer
  ```

## AWS Setup

If you are using AWS, follow these steps:

1. **Install AWS CLI**
   - For macOS, download and run the PKG installer from the [AWS CLI website](https://aws.amazon.com/cli/).

2. **Create an IAM User**
   - Create an IAM user and attach the `ReadOnlyAccess` policy.
   - Follow the instructions in the [IAM User Guide](https://docs.aws.amazon.com/IAM/latest/UserGuide/id_users_create.html).

3. **Configure AWS CLI**
   - Run the following command and enter the `Access Key ID` and `Secret Access Key` when prompted. You can leave the `region` and `output format` fields blank by pressing Enter.

     ```bash
     aws configure
     ```

   - Refer to the [AWS CLI User Guide](https://docs.aws.amazon.com/cli/latest/userguide/welcome-examples.html) for more details.

4. **Verify Authentication File**
   - Ensure that `~/.aws/credentials` is created and that the desired account is set as `[default]`.
   - Only the `[default]` profile will be used to prevent unintended account access.

## GCP Setup

If you are using GCP, follow these steps:

1. **Install Google Cloud SDK (gcloud)**
   - Download and extract the Google Cloud SDK from the [installation page](https://cloud.google.com/sdk/docs/install).
   - Add the SDK to your PATH by running the installation script:

     ```bash
     ./google-cloud-sdk/install.sh
     ```

2. **Configure gcloud CLI**
   - Ensure you are using a Google account that has access to the target project.
   - Initialize the gcloud CLI:

     ```bash
     gcloud init
     ```

   - Authenticate with gcloud:

     ```bash
     gcloud auth login
     ```

   - Authenticate for API usage:

     ```bash
     gcloud auth application-default login
     ```

   - For more information, refer to the [Authorization Documentation](https://cloud.google.com/sdk/docs/authorizing?hl=en) and the [gcloud auth documentation](https://cloud.google.com/sdk/gcloud/reference/auth/application-default/login).

3. **Create a Service Account and Obtain Keys**
   - Follow the steps in the [Creating and Managing Service Account Keys](https://cloud.google.com/iam/docs/creating-managing-service-account-keys?hl=en) guide to create a service account and download its key.

4. **Grant Permissions to the Service Account**
   - In the IAM console, assign the following roles to the service account:
     - Cloud Functions Viewer
     - Compute Viewer
     - Service Account Token Creator
     - Viewer

5. **Enable APIs for Your Project**
   - Identify your project ID:

     ```bash
     gcloud projects list
     ```

   - Set your project ID:

     ```bash
     gcloud config set project YOUR_PROJECT_ID
     ```

   - Enable the necessary APIs. Since enabling more than 20 APIs at once can cause errors, enable them in batches of 11:

     **First Batch:**

     ```bash
     gcloud services enable \
       compute.googleapis.com \
       cloudresourcemanager.googleapis.com \
       iam.googleapis.com \
       monitoring.googleapis.com \
       logging.googleapis.com \
       cloudfunctions.googleapis.com \
       pubsub.googleapis.com \
       run.googleapis.com \
       cloudbuild.googleapis.com \
       bigquery.googleapis.com \
       artifactregistry.googleapis.com
     ```

     **Second Batch:**

     ```bash
     gcloud services enable \
       dataform.googleapis.com \
       dataproc.googleapis.com \
       sqladmin.googleapis.com \
       container.googleapis.com \
       cloudkms.googleapis.com \
       redis.googleapis.com \
       dns.googleapis.com \
       cloudscheduler.googleapis.com \
       cloudasset.googleapis.com \
       serviceusage.googleapis.com \
       cloudtasks.googleapis.com
     ```

Ensure all the APIs required for your project are enabled successfully.

## Next Step (Getting Started with Yogaya CLI)

Once you have completed the pre-installation steps above, you can proceed to install and start using the Yogaya CLI.
Refer to the [guide.md](./guide.md) for detailed instructions on usage.
