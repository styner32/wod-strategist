# IAM permissions for the Cloud Run Service Account

# 1. Allow the Service Account to manage storage objects in the uploads bucket
resource "google_storage_bucket_iam_member" "run_sa_storage_admin" {
  bucket = google_storage_bucket.uploads.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.run_sa.email}"
}

# 2. Allow the Service Account to sign blobs (Required for Signed URLs)
# Granting at the Project level ensures the permission is correctly picked up by the Cloud Run runtime.
resource "google_project_iam_member" "run_sa_token_creator" {
  project = var.project_id
  role    = "roles/iam.serviceAccountTokenCreator"
  member  = "serviceAccount:${google_service_account.run_sa.email}"
}
