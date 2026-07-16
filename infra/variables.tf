variable "project_id" {
  description = "The Google Cloud Project ID"
  type        = string
}

variable "region" {
  description = "The Google Cloud region to deploy to"
  type        = string
  default     = "asia-northeast3"
}

variable "app_name" {
  description = "The name of the application"
  type        = string
  default     = "wod-strategist"
}

variable "environment" {
  description = "The deployment environment (e.g., dev, prod)"
  type        = string
  default     = "dev"
}

variable "db_password" {
  description = "The password for the PostgreSQL database user"
  type        = string
  sensitive   = true
}

variable "gemini_api_key" {
  description = "API Key for Gemini AI"
  type        = string
  sensitive   = true
}

variable "api_secret" {
  description = "Secret key for securing API endpoints"
  type        = string
  sensitive   = true
}

variable "local_static_origins" {
  description = "Origins allowed to access the uploads bucket directly for local QA tooling"
  type        = list(string)
  default     = ["http://localhost:3000", "http://127.0.0.1:3000"]
}

variable "jwt_secret" {
  description = "Secret key for signing JWT tokens"
  type        = string
  sensitive   = true
}

variable "domain_name" {
  description = "Apex domain served via Cloudflare"
  type        = string
  default     = "formbuddy.fit"
}

variable "cloudflare_api_token" {
  description = "Cloudflare API token with Zone:DNS:Edit scoped to the domain"
  type        = string
  sensitive   = true
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone ID for the domain"
  type        = string
}

variable "admin_cidr" {
  description = "CIDR for Cloud SQL authorized network (admin home IP)"
  type        = string
  default     = "49.172.61.41/32"
}

variable "pipeline_mode" {
  description = "The pipeline mode for video analysis (legacy, optimized, compare)"
  type        = string
  default     = "legacy"
}

variable "enable_chunk_reanalysis" {
  description = "Enable exact-interval chunk re-analysis features (default false for safety and quota control)"
  type        = bool
  default     = false
}

variable "enable_session_reanalysis" {
  description = "Enable session re-analysis features (default false for safety and quota control)"
  type        = bool
  default     = false
}

