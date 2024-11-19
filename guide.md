# Using Yogaya CLI

Follow the steps below to execute commands using the Yogaya CLI. These instructions abstract individual path settings to ensure clarity and flexibility.

## Installation
```bash
brew tap Yogaya-Inc/yogaya
brew install yogaya
```

## Yogaya Commands

### 1. `yogaya init`

Initializes the Yogaya configuration by creating necessary configuration files.

**Usage:**
```bash
yogaya init [Configuration_File_Creation_Location_Path(Optional)]
```

- **Without Arguments:**
  - Runs in the current directory.
  - Creates a `.yogaya` directory in the current directory.

  **Example:**
  ```bash
  yogaya init
  ```

- **With Path Argument:**
  - Creates a `.yogaya` directory under the specified path.

  **Example:**
  ```bash
  yogaya init /path/to/directory
  ```

**What It Does:**
- Creates a `.yogaya` directory containing:
  - `cloud_accounts.conf`: Used for authentication.
  - `tenant.conf`: Currently has no specific function.

### 2. `yogaya add`

Adds a cloud service account to Yogaya CLI by setting the authentication credentials.

**Usage:**
```bash
yogaya add <Cloud_Service_Name> <cloud_accounts.conf_Path> <Cloud_Service_Credential_Path>
```

- **Parameters:**
  - `<Cloud_Service_Name>`: The name of the cloud service (e.g., `aws`, `gcp`).
  - `<cloud_accounts.conf_Path>`: Path to the `cloud_accounts.conf` file.
  - `<Cloud_Service_Credential_Path>`: Path to the cloud service's credential file.

**Examples:**
- **Adding a GCP Account:**
  ```bash
  yogaya add gcp ./yogaya/.yogaya/cloud_accounts.conf /path/to/gcp-service-account.json
  ```

- **Adding an AWS Account:**
  ```bash
  yogaya add aws ./yogaya/.yogaya/cloud_accounts.conf /path/to/aws/credentials
  ```

**What It Does:**
- Associates the specified cloud service credentials with Yogaya CLI by updating the `cloud_accounts.conf` file.

### 3. `yogaya generate`

Generates and retrieves resources from the added cloud accounts.

**Usage:**
```bash
yogaya generate <cloud_accounts.conf_Path>
```

- **Parameters:**
  - `<cloud_accounts.conf_Path>`: Path to the `cloud_accounts.conf` file containing the added accounts.

**Example:**
```bash
yogaya generate ./yogaya/.yogaya/cloud_accounts.conf
```

**What It Does:**
- Utilizes the accounts specified in `cloud_accounts.conf` to retrieve resources.
- Creates a `generated` directory in the current working directory.
- Outputs the retrieved resources into the `generated` directory.

## Example Workflow

1. **Initialize Yogaya Configuration:**
   ```bash
   yogaya init /path/to/configuration
   ```

2. **Add Cloud Service Accounts:**
   - **GCP:**
     ```bash
     yogaya add gcp /path/to/configuration/.yogaya/cloud_accounts.conf /path/to/gcp-service-account.json
     ```
   - **AWS:**
     ```bash
     yogaya add aws /path/to/configuration/.yogaya/cloud_accounts.conf /path/to/aws/credentials
     ```

3. **Generate Resources:**
   ```bash
   yogaya generate /path/to/configuration/.yogaya/cloud_accounts.conf
   ```

After executing these commands, the `generated` directory will contain the resources retrieved from your specified cloud accounts.
