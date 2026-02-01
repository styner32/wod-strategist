output "api_url" {
  value = google_cloud_run_v2_service.api.uri
}

output "artifact_registry_repo" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.repo.name}"
}

output "db_instance_connection_name" {
  value = google_sql_database_instance.postgres.connection_name
}

output "vpc_connector" {
  value = google_vpc_access_connector.connector.id
}
