# yogaya-cli

## Overview

CLI Tools

- Get cloud resources by regions
- Generate Terraform configurations from existing cloud infrastructure
- Support for multiple cloud providers (AWS, GCP)

> ⚠️ WARNING: .gitignore is currently under development. Do not commit executable files or execution result files.

## Requirements

- Go 1.23.3 darwin/arm64 or later
- Terraform v1.9.8
- Terraformer v0.8.24
- Git 2.39.3 (Apple Git-146) or later

For detailed requirements, see [pre-installation.md](./pre-installation.md).

## Installation

### 1. Install Go

```bash
brew install go
```

> Go has backward version compatibility, so having the latest version installed will allow you to run older code and executables.

Verify the installation:

```bash
go version
```

> If the version is displayed, the installation is complete.

### 2. Package Installation

```bash
go mod tidy
```

> This command will automatically download and install all necessary dependencies defined in the project.

### 3. Build Instructions

1. Navigate to the project directory:

   ```bash
   cd ./yogaya-cli
    ```

2. Build the project:

   ```bash
   go build -o yogaya ./main.go
    ```

> This will generate an executable file (yogaya) in the repository.
>
> Command structure:
> `go build -o <executable_name> <build_target>`
>
> While it might seem like the build target should specify the entire project,
> specifying just main.go is sufficient for building.
>
> To change the executable name, modify the 'yogaya' argument in the command.

## Running the Built Executable

### You can run the executable in two ways

- Create .yogaya directory in current directory:

   ````bash
   ./yogaya init ./
    ````

- Create .yogaya directory in home directory:

   ```bash
   ./yogaya init
    ```

### Generated Directory Structure

```tree
.yogaya/
├── cloud_accounts.conf (Used for authentication)
├── tenant.conf: Currently has no specific function. #[1]
└── .git/ (git initialization directory)

[1]: Contains tenant key in format `tenant_key=<current local time converted to hexadecimal>`
```

For detailed running, see [guide.md](./guide.md).
