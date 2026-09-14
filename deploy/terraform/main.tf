terraform {
  required_version = ">= 1.5.0"
  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0.0"
    }
  }
}

variable "project_id" {
  type        = string
  default     = "apigee-x-jog"
  description = "Google Cloud Project ID"
}

variable "region" {
  type        = string
  default     = "europe-west1"
  description = "Google Cloud Region"
}

variable "vpc_network" {
  type        = string
  default     = "vpc-customer-apigee-x"
  description = "VPC Network name"
}

variable "subnet_name" {
  type        = string
  default     = "sub-customer-apigee-x"
  description = "Primary private subnet name"
}

variable "cloud_run_service_name" {
  type        = string
  default     = "websocket-echo"
  description = "Cloud Run WebSocket Echo service name"
}

# 1. Proxy-Only Subnet for Regional Internal Application Load Balancer
resource "google_compute_subnetwork" "proxy_only" {
  name          = "proxy-only-subnet-ew1"
  project       = var.project_id
  region        = var.region
  network       = var.vpc_network
  ip_cidr_range = "10.129.0.0/23"
  purpose       = "REGIONAL_MANAGED_PROXY"
  role          = "ACTIVE"
}

# 2. Private Service Connect NAT Subnet for Service Attachment
resource "google_compute_subnetwork" "psc_nat" {
  name          = "psc-nat-subnet-ws-echo"
  project       = var.project_id
  region        = var.region
  network       = var.vpc_network
  ip_cidr_range = "192.168.2.0/24"
  purpose       = "PRIVATE_SERVICE_CONNECT"
}

# 3. Serverless NEG pointing to Cloud Run
resource "google_compute_region_network_endpoint_group" "serverless_neg" {
  name                  = "websocket-echo-neg"
  project               = var.project_id
  region                = var.region
  network_endpoint_type = "SERVERLESS"

  cloud_run {
    service = var.cloud_run_service_name
  }
}

# 4. Regional Backend Service (INTERNAL_MANAGED, HTTPS)
# Note: Serverless NEGs inherit their request/WebSocket timeout directly from the Cloud Run service
resource "google_compute_region_backend_service" "ilb_backend" {
  name                  = "websocket-echo-ilb-backend"
  project               = var.project_id
  region                = var.region
  load_balancing_scheme = "INTERNAL_MANAGED"
  protocol              = "HTTPS"

  backend {
    group           = google_compute_region_network_endpoint_group.serverless_neg.id
    balancing_mode  = "UTILIZATION"
    capacity_scaler = 1.0
  }
}

# 5. Regional URL Map
resource "google_compute_region_url_map" "ilb_urlmap" {
  name            = "websocket-echo-ilb-urlmap"
  project         = var.project_id
  region          = var.region
  default_service = google_compute_region_backend_service.ilb_backend.id
}

# 6. Self-Signed SSL Certificate for Internal HTTPS LB
resource "tls_private_key" "ilb_key" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "tls_self_signed_cert" "ilb_cert" {
  private_key_pem = tls_private_key.ilb_key.private_key_pem

  subject {
    common_name  = "websocket-echo.internal"
    organization = "Apigee WebSocket Echo"
  }

  dns_names             = ["websocket-echo.internal", "*.internal"]
  validity_period_hours = 87600 # 10 years

  allowed_uses = [
    "key_encipherment",
    "digital_signature",
    "server_auth",
  ]
}

resource "google_compute_region_ssl_certificate" "ilb_ssl_cert" {
  name        = "websocket-echo-ilb-cert"
  project     = var.project_id
  region      = var.region
  private_key = tls_private_key.ilb_key.private_key_pem
  certificate = tls_self_signed_cert.ilb_cert.cert_pem
}

# 7. Regional Target HTTPS Proxy
resource "google_compute_region_target_https_proxy" "ilb_https_proxy" {
  name             = "websocket-echo-ilb-https-proxy"
  project          = var.project_id
  region           = var.region
  url_map          = google_compute_region_url_map.ilb_urlmap.id
  ssl_certificates = [google_compute_region_ssl_certificate.ilb_ssl_cert.id]
}

# 8. Static Regional Internal IP Address
resource "google_compute_address" "ilb_ip" {
  name         = "websocket-echo-ilb-ip"
  project      = var.project_id
  region       = var.region
  subnetwork   = var.subnet_name
  address_type = "INTERNAL"
  purpose      = "GCE_ENDPOINT"
}

# 9. Regional Forwarding Rule (INTERNAL_MANAGED)
resource "google_compute_forwarding_rule" "ilb_forwarding_rule" {
  name                  = "websocket-echo-ilb-forwarding-rule"
  project               = var.project_id
  region                = var.region
  load_balancing_scheme = "INTERNAL_MANAGED"
  network               = var.vpc_network
  subnetwork            = var.subnet_name
  ip_address            = google_compute_address.ilb_ip.id
  ip_protocol           = "TCP"
  port_range            = "443"
  target                = google_compute_region_target_https_proxy.ilb_https_proxy.id

  depends_on = [google_compute_subnetwork.proxy_only]
}

# 10. Private Service Connect Service Attachment (PSC Producer)
resource "google_compute_service_attachment" "psc_service_attachment" {
  name                  = "websocket-echo-service-attachment"
  project               = var.project_id
  region                = var.region
  connection_preference = "ACCEPT_AUTOMATIC"
  nat_subnets           = [google_compute_subnetwork.psc_nat.id]
  target_service        = google_compute_forwarding_rule.ilb_forwarding_rule.id
}

# 11. Apigee Endpoint Attachment (PSC Consumer)
resource "google_apigee_endpoint_attachment" "apigee_ea" {
  org_id                 = "organizations/${var.project_id}"
  endpoint_attachment_id = "websocket-echo-ea"
  location               = var.region
  service_attachment     = google_compute_service_attachment.psc_service_attachment.id
}

output "ilb_ip_address" {
  description = "Internal IP address of the Regional Internal Application Load Balancer"
  value       = google_compute_address.ilb_ip.address
}

output "service_attachment_uri" {
  description = "Private Service Connect Service Attachment URI"
  value       = google_compute_service_attachment.psc_service_attachment.id
}

output "apigee_endpoint_attachment_host" {
  description = "Private IP/host assigned to the Apigee Endpoint Attachment"
  value       = google_apigee_endpoint_attachment.apigee_ea.host
}
