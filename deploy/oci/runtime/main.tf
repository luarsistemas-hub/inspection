resource "oci_core_vcn" "inspection" {
  compartment_id = var.compartment_ocid
  cidr_blocks    = [var.vcn_cidr]
  display_name   = "inspection-vcn"
  dns_label      = "inspection"
  freeform_tags  = var.tags
}

resource "oci_core_internet_gateway" "inspection" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.inspection.id
  display_name   = "inspection-internet-gateway"
}

resource "oci_core_route_table" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.inspection.id
  display_name   = "inspection-public-routes"
  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.inspection.id
  }
}

resource "oci_core_security_list" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.inspection.id
  display_name   = "inspection-subnet-default-deny"
  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }
}

resource "oci_core_network_security_group" "runtime" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.inspection.id
  display_name   = "inspection-runtime"
}

resource "oci_core_network_security_group_security_rule" "https" {
  network_security_group_id = oci_core_network_security_group.runtime.id
  direction                 = "INGRESS"
  protocol                  = "6"
  source                    = "0.0.0.0/0"
  source_type               = "CIDR_BLOCK"
  tcp_options {
    destination_port_range {
      min = 443
      max = 443
    }
  }
}

resource "oci_core_network_security_group_security_rule" "ssh" {
  network_security_group_id = oci_core_network_security_group.runtime.id
  direction                 = "INGRESS"
  protocol                  = "6"
  source                    = var.admin_cidr
  source_type               = "CIDR_BLOCK"
  tcp_options {
    destination_port_range {
      min = 22
      max = 22
    }
  }
}

resource "oci_core_network_security_group_security_rule" "wireguard_ssh" {
  network_security_group_id = oci_core_network_security_group.runtime.id
  direction                 = "INGRESS"
  protocol                  = "6"
  source                    = "10.77.0.2/32"
  source_type               = "CIDR_BLOCK"
  tcp_options {
    destination_port_range {
      min = 22
      max = 22
    }
  }
}

resource "oci_core_network_security_group_security_rule" "wireguard" {
  network_security_group_id = oci_core_network_security_group.runtime.id
  direction                 = "INGRESS"
  protocol                  = "17"
  source                    = "0.0.0.0/0"
  source_type               = "CIDR_BLOCK"
  udp_options {
    destination_port_range {
      min = var.wireguard_port
      max = var.wireguard_port
    }
  }
}

resource "oci_core_network_security_group_security_rule" "egress" {
  network_security_group_id = oci_core_network_security_group.runtime.id
  direction                 = "EGRESS"
  protocol                  = "all"
  destination               = "0.0.0.0/0"
  destination_type          = "CIDR_BLOCK"
}

resource "oci_core_subnet" "public" {
  compartment_id             = var.compartment_ocid
  vcn_id                     = oci_core_vcn.inspection.id
  cidr_block                 = var.subnet_cidr
  display_name               = "inspection-public"
  dns_label                  = "runtime"
  route_table_id             = oci_core_route_table.public.id
  security_list_ids          = [oci_core_security_list.public.id]
  prohibit_public_ip_on_vnic = false
}

resource "oci_core_volume" "data" {
  compartment_id      = var.compartment_ocid
  availability_domain = var.availability_domain
  display_name        = "inspection-data"
  size_in_gbs         = var.data_volume_gb
  freeform_tags       = var.tags
  lifecycle { prevent_destroy = true }
}

resource "oci_core_instance" "runtime" {
  compartment_id      = var.compartment_ocid
  availability_domain = var.availability_domain
  display_name        = var.instance_name
  shape               = "VM.Standard.A1.Flex"
  freeform_tags       = var.tags
  shape_config {
    ocpus         = var.shape_ocpus
    memory_in_gbs = var.shape_memory_gb
  }
  source_details {
    source_type             = "image"
    source_id               = var.image_ocid
    boot_volume_size_in_gbs = var.boot_volume_gb
  }
  create_vnic_details {
    subnet_id        = oci_core_subnet.public.id
    assign_public_ip = true
    nsg_ids          = [oci_core_network_security_group.runtime.id]
    display_name     = "inspection-runtime-vnic"
  }
  metadata = { ssh_authorized_keys = var.ssh_public_key }
  agent_config {
    are_all_plugins_disabled = false
    plugins_config {
      name          = "Compute Instance Monitoring"
      desired_state = "ENABLED"
    }
  }
}

resource "oci_core_volume_attachment" "data" {
  attachment_type = "paravirtualized"
  instance_id     = oci_core_instance.runtime.id
  volume_id       = oci_core_volume.data.id
  display_name    = "inspection-data"
  device          = "/dev/oracleoci/oraclevdb"
}

resource "oci_identity_policy" "runtime_secrets" {
  compartment_id = var.tenancy_ocid
  description    = "Permit the Inspection instance to read only the listed Vault secret bundles"
  name           = "inspection-runtime-secret-access"
  statements = [
    "Allow dynamic-group inspection-runtime to read secret-bundles in compartment id ${var.compartment_ocid} where any {${local.secret_policy_conditions}}"
  ]
}

resource "oci_monitoring_alarm" "cpu" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-cpu-high"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "oci_computeagent"
  query                 = "CpuUtilization[1m]{resourceId = \"${oci_core_instance.runtime.id}\"}.mean() > 85"
  severity              = "WARNING"
  pending_duration      = "PT15M"
  resolution            = "1m"
  body                  = "Inspection VM CPU has exceeded 85% for 15 minutes."
}

resource "oci_monitoring_alarm" "memory" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-memory-high"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "oci_computeagent"
  query                 = "MemoryUtilization[1m]{resourceId = \"${oci_core_instance.runtime.id}\"}.mean() > 85"
  severity              = "WARNING"
  pending_duration      = "PT15M"
  resolution            = "1m"
  body                  = "Inspection VM memory has exceeded 85% for 15 minutes."
}

resource "oci_monitoring_alarm" "object_storage" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-object-storage-high"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "oci_objectstorage"
  query                 = "StoredBytes[1h]{resourceDisplayName = \"${var.bucket_name}\"}.mean() > 17179869184"
  severity              = "WARNING"
  pending_duration      = "PT1H"
  resolution            = "1m"
  body                  = "Inspection Object Storage bucket has exceeded 16 GiB."
}

resource "oci_monitoring_alarm" "object_storage_critical" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-object-storage-18-gib"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "oci_objectstorage"
  query                 = "StoredBytes[1h]{resourceDisplayName = \"${var.bucket_name}\"}.mean() > 19327352832"
  severity              = "CRITICAL"
  pending_duration      = "PT1H"
  resolution            = "1m"
  body                  = "Inspection Object Storage bucket has exceeded 18 GiB; stop growth and review eligible objects."
}

resource "oci_monitoring_alarm" "disk_warning" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-data-volume-80-percent"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "inspection_host"
  query                 = "DataVolumeUsedPercent[1m]{instanceId = \"${oci_core_instance.runtime.id}\"}.mean() > 80"
  severity              = "WARNING"
  pending_duration      = "PT1M"
  resolution            = "1m"
  body                  = "Inspection data volume has exceeded 80% usage."
}

resource "oci_monitoring_alarm" "disk_critical" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-data-volume-90-percent"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "inspection_host"
  query                 = "DataVolumeUsedPercent[1m]{instanceId = \"${oci_core_instance.runtime.id}\"}.mean() > 90"
  severity              = "CRITICAL"
  pending_duration      = "PT1M"
  resolution            = "1m"
  body                  = "Inspection data volume has exceeded 90% usage; protect database integrity and free capacity safely."
}

resource "oci_monitoring_alarm" "heartbeat" {
  compartment_id        = var.compartment_ocid
  destinations          = [var.alert_topic_ocid]
  display_name          = "inspection-host-heartbeat-missing"
  is_enabled            = true
  metric_compartment_id = var.compartment_ocid
  namespace             = "inspection_host"
  query                 = "HostHeartbeat[1m]{instanceId = \"${oci_core_instance.runtime.id}\"}.groupBy(instanceId).absent(4m)"
  severity              = "CRITICAL"
  pending_duration      = "PT1M"
  resolution            = "1m"
  body                  = "Inspection host heartbeat has been absent for five minutes."
}

output "instance_ocid" { value = oci_core_instance.runtime.id }
output "public_ip" { value = oci_core_instance.runtime.public_ip }
output "data_volume_ocid" { value = oci_core_volume.data.id }
output "vcn_ocid" { value = oci_core_vcn.inspection.id }
output "subnet_ocid" { value = oci_core_subnet.public.id }
output "nsg_ocid" { value = oci_core_network_security_group.runtime.id }
output "bucket_name" { value = var.bucket_name }
output "namespace" { value = var.namespace }
