variable "region" {
  type = string
}

variable "tenancy_ocid" {
  type = string
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
