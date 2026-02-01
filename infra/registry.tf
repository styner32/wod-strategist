# Artifact Registry
resource "google_artifact_registry_repository" "repo" {
  location      = var.region
  repository_id = "${var.app_name}-repo-${var.environment}"
  description   = "Docker repository for ${var.app_name}"
  format        = "DOCKER"
  depends_on    = [google_project_service.apis]
}
