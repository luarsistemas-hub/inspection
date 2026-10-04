data "oci_objectstorage_namespace" "tenant" {
  compartment_id = var.tenancy_ocid
}

resource "oci_identity_compartment" "inspection" {
  compartment_id = var.tenancy_ocid
  description    = "Inspection production resources"
  name           = var.compartment_name
  freeform_tags  = var.tags
}

resource "oci_kms_vault" "inspection" {
  compartment_id = oci_identity_compartment.inspection.id
  display_name   = "inspection-vault"
  vault_type     = "DEFAULT"
  freeform_tags  = var.tags
  lifecycle { prevent_destroy = true }
}

resource "oci_kms_key" "inspection" {
  compartment_id      = oci_identity_compartment.inspection.id
  display_name        = "inspection-secrets-key"
  management_endpoint = oci_kms_vault.inspection.management_endpoint
  protection_mode     = "SOFTWARE"
  key_shape {
    algorithm = "AES"
    length    = 32
  }
  freeform_tags = var.tags
  lifecycle { prevent_destroy = true }
}

resource "oci_objectstorage_bucket" "application" {
  compartment_id = oci_identity_compartment.inspection.id
  namespace      = data.oci_objectstorage_namespace.tenant.namespace
  name           = var.bucket_name
  access_type    = "NoPublicAccess"
  storage_tier   = "Standard"
  versioning     = "Disabled"
  freeform_tags  = var.tags
  lifecycle { prevent_destroy = true }
}

resource "oci_identity_user" "s3" {
  compartment_id = var.tenancy_ocid
  description    = "Scoped S3-compatible access for Inspection"
  name           = "inspection-objectstore"
}

resource "oci_identity_group" "s3" {
  compartment_id = var.tenancy_ocid
  description    = "Inspection object storage access"
  name           = "inspection-objectstore"
}

resource "oci_identity_user_group_membership" "s3" {
  group_id = oci_identity_group.s3.id
  user_id  = oci_identity_user.s3.id
}

resource "oci_identity_dynamic_group" "runtime" {
  compartment_id = var.tenancy_ocid
  description    = "Inspection runtime instances in the dedicated compartment"
  matching_rule  = "All {instance.compartment.id = '${oci_identity_compartment.inspection.id}'}"
  name           = "inspection-runtime"
}

resource "oci_identity_policy" "inspection" {
  compartment_id = var.tenancy_ocid
  description    = "Least-scope policies for the dedicated Inspection compartment"
  name           = "inspection-runtime-access"
  statements = [
    "Allow group ${oci_identity_group.s3.name} to manage objects in compartment ${oci_identity_compartment.inspection.name} where target.bucket.name = '${var.bucket_name}'",
    "Allow group ${oci_identity_group.s3.name} to read buckets in compartment ${oci_identity_compartment.inspection.name} where target.bucket.name = '${var.bucket_name}'",
    "Allow dynamic-group ${oci_identity_dynamic_group.runtime.name} to use keys in compartment ${oci_identity_compartment.inspection.name} where target.key.id = '${oci_kms_key.inspection.id}'",
    "Allow dynamic-group ${oci_identity_dynamic_group.runtime.name} to use ons-topics in compartment ${oci_identity_compartment.inspection.name}",
    "Allow dynamic-group ${oci_identity_dynamic_group.runtime.name} to use metrics in compartment ${oci_identity_compartment.inspection.name}"
  ]
}

resource "oci_ons_notification_topic" "alerts" {
  compartment_id = oci_identity_compartment.inspection.id
  name           = "inspection-operations"
  description    = "Inspection capacity and availability alerts"
  freeform_tags  = var.tags
}

resource "oci_ons_subscription" "alerts_email" {
  compartment_id = oci_identity_compartment.inspection.id
  endpoint       = var.alert_email
  protocol       = "EMAIL"
  topic_id       = oci_ons_notification_topic.alerts.id
}

resource "oci_email_sender" "inspection" {
  compartment_id = oci_identity_compartment.inspection.id
  email_address  = var.email_sender_address
  freeform_tags  = var.tags
}

output "compartment_ocid" { value = oci_identity_compartment.inspection.id }
output "namespace" { value = data.oci_objectstorage_namespace.tenant.namespace }
output "bucket_name" { value = oci_objectstorage_bucket.application.name }
output "s3_user_ocid" { value = oci_identity_user.s3.id }
output "vault_ocid" { value = oci_kms_vault.inspection.id }
output "vault_management_endpoint" { value = oci_kms_vault.inspection.management_endpoint }
output "vault_crypto_endpoint" { value = oci_kms_vault.inspection.crypto_endpoint }
output "alert_topic_ocid" { value = oci_ons_notification_topic.alerts.id }
output "email_sender_ocid" { value = oci_email_sender.inspection.id }
output "email_sender_state" { value = oci_email_sender.inspection.state }
