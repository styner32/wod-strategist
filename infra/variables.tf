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