variable "region" { type = string }
variable "tenancy_ocid" { type = string }
variable "compartment_ocid" { type = string }
variable "ssh_public_key" { type = string }
variable "admin_cidr" { type = string }
variable "wireguard_port" {
  description = "UDP listener for the authenticated GitHub Actions WireGuard peer."
  type        = number
  default     = 51820
  validation {
    condition     = var.wireguard_port >= 1024 && var.wireguard_port <= 65535
    error_message = "WireGuard must use an unprivileged UDP port."
  }
}
variable "image_ocid" { type = string }
variable "availability_domain" { type = string }
variable "instance_name" {
  type    = string
  default = "inspection-runtime"
}
variable "vcn_cidr" {
  type    = string
  default = "10.20.0.0/16"
}
variable "subnet_cidr" {
  type    = string
  default = "10.20.1.0/24"
}
variable "bucket_name" {
  type    = string
  default = "inspection-private"
}
variable "namespace" { type = string }
variable "alert_topic_ocid" { type = string }
variable "secret_ocids" {
  description = "Map of approved runtime secret names to OCI Vault secret OCIDs."
  type        = map(string)
  validation {
    condition     = length(var.secret_ocids) > 0 && alltrue([for secret_id in values(var.secret_ocids) : startswith(secret_id, "ocid1.vaultsecret.")])
    error_message = "Provide the OCI Vault OCIDs for the explicitly approved runtime secrets."
  }
}
locals {
  secret_policy_conditions = join(", ", [for secret_id in values(var.secret_ocids) : "target.secret.id = '${secret_id}'"])
}
variable "tags" {
  type    = map(string)
  default = { project = "inspection", managed-by = "terraform" }
}

variable "shape_ocpus" {
  type    = number
  default = 2
  validation {
    condition     = var.shape_ocpus == 2
    error_message = "Always Free profile is fixed at 2 OCPUs."
  }
}

variable "shape_memory_gb" {
  type    = number
  default = 12
  validation {
    condition     = var.shape_memory_gb == 12
    error_message = "Always Free profile is fixed at 12 GB."
  }
}

variable "boot_volume_gb" {
  type    = number
  default = 50
  validation {
    condition     = var.boot_volume_gb == 50
    error_message = "Boot volume profile is fixed at 50 GB."
  }
}

variable "data_volume_gb" {
  type    = number
  default = 150
  validation {
    condition     = var.data_volume_gb == 150
    error_message = "Data volume profile is fixed at 150 GB."
  }
}
