variable "region" {
  type = string
}

variable "tenancy_ocid" {
  type = string
}

variable "additional_runtime_instance_ocid" {
  description = "Optional existing runtime instance to include until it is moved into the dedicated compartment."
  type        = string
  default     = ""
  validation {
    condition     = var.additional_runtime_instance_ocid == "" || startswith(var.additional_runtime_instance_ocid, "ocid1.instance.")
    error_message = "Provide an OCI compute instance OCID or leave this value empty."
  }
}

variable "compartment_name" {
  type    = string
  default = "inspection"
}

variable "bucket_name" {
  type    = string
  default = "inspection-private"
}

variable "alert_email" {
  type = string
}

variable "email_sender_address" {
  description = "Non-secret approved From address registered with OCI Email Delivery."
  type        = string
}

variable "tags" {
  type    = map(string)
  default = { project = "inspection", managed-by = "terraform" }
}
