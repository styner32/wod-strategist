# Cloud Run Service Account
resource "google_service_account" "run_sa" {
  account_id   = "${var.app_name}-run-sa-${var.environment}"
  display_name = "Cloud Run Service Account"
}

# Grant secrets access if we use Secret Manager (optional but recommended)
# For now, we inject via env vars directly, but SA is good practice.

# API Service
resource "google_cloud_run_v2_service" "api" {
  name     = "${var.app_name}-api-${var.environment}"
  location = var.region
  ingress = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.run_sa.email

    vpc_access{
      connector = google_vpc_access_connector.connector.id
      egress    = "PRIVATE_RANGES_ONLY"
    }

    containers {
      # Use a placeholder image initially so `terraform apply` works.
      # User must build/push the real image later.
      image = "asia-northeast3-docker.pkg.dev/gen-lang-client-0826771503/wod-strategist-repo-dev/api:latest"
      
      ports {
        container_port = 8080
      }

      env {
        name  = "DATABASE_URL"
        value = "postgres://appuser:${var.db_password}@${google_sql_database_instance.postgres.private_ip_address}:5432/${google_sql_database.database.name}?sslmode=disable"
      }
      env {
        name  = "REDIS_URL"
        value = "${google_redis_instance.cache.host}:${google_redis_instance.cache.port}"
      }
      env {
        name  = "GEMINI_API_KEY"
        value = var.gemini_api_key
      }
      env {
        name  = "API_SECRET"
        value = var.api_secret
      }
      env {
        name  = "GCS_BUCKET_NAME"
        value = google_storage_bucket.uploads.name
      }
    }
  }

  depends_on = [
    google_sql_database_instance.postgres,
    google_redis_instance.cache
  ]
}

# Worker Pool (for background tasks)
resource "google_cloud_run_v2_worker_pool" "worker" {
  provider = google-beta
  name     = "${var.app_name}-worker-${var.environment}"
  location = var.region
  launch_stage = "BETA"

  template {
    service_account = google_service_account.run_sa.email

    vpc_access {
      network_interfaces {
        network    = google_compute_network.vpc.name
        subnetwork = google_compute_subnetwork.subnet.name
      }
      egress = "PRIVATE_RANGES_ONLY" 
    }

    containers {
      image = "asia-northeast3-docker.pkg.dev/gen-lang-client-0826771503/wod-strategist-repo-dev/worker:latest"
      
      env {
        name  = "DATABASE_URL"
        value = "postgres://appuser:${var.db_password}@${google_sql_database_instance.postgres.private_ip_address}:5432/${google_sql_database.database.name}?sslmode=disable"
      }
      env {
        name  = "REDIS_URL"
        value = "${google_redis_instance.cache.host}:${google_redis_instance.cache.port}"
      }
      env {
        name  = "GEMINI_API_KEY"
        value = var.gemini_api_key
      }
      env {
        name  = "GCS_BUCKET_NAME"
        value = google_storage_bucket.uploads.name
      }
    }
  }
  
  depends_on = [
    google_sql_database_instance.postgres,
    google_redis_instance.cache
  ]
}

# Public access for API
resource "google_cloud_run_service_iam_member" "public_access" {
  service  = google_cloud_run_v2_service.api.name
  location = google_cloud_run_v2_service.api.location
  role     = "roles/run.invoker"
  member   = "allUsers"
}
