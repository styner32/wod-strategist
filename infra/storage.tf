resource "google_storage_bucket" "uploads" {
  name          = "${var.app_name}-uploads-${var.environment}"
  location      = var.region
  force_destroy = var.environment == "prod" ? true : false

  uniform_bucket_level_access = true

  cors {
    origin          = var.local_static_origins
    method          = ["GET", "HEAD", "PUT", "OPTIONS"]
    response_header = ["Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "x-goog-resumable"]
    max_age_seconds = 3600
  }

  # Delete files older than 1 day to manage costs/storage
  lifecycle_rule {
    condition {
      age = 1
    }
    action {
      type = "Delete"
    }
  }
}

# Grant Cloud Run Service Account access to the bucket
resource "google_storage_bucket_iam_member" "run_sa_admin" {
  bucket = google_storage_bucket.uploads.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.run_sa.email}"
}
