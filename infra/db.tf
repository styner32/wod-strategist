# Cloud SQL (PostgreSQL)
resource "google_sql_database_instance" "postgres" {
  name             = "${var.app_name}-db-${var.environment}"
  database_version = "POSTGRES_15" # 18 is not yet standard in all TF providers/GCP regions, 15 is safe stable
  region           = var.region
  deletion_protection = var.environment == "prod" ? true : false

  settings {
    tier = "db-f1-micro" # Use minimal tier for dev/cost savings
    ip_configuration {
      ipv4_enabled    = true
      private_network = google_compute_network.vpc.id

      authorized_networks {
        name  = "admin-home"
        value = var.admin_cidr
      }
    }
  }

  depends_on = [google_service_networking_connection.private_vpc_connection]
}

resource "google_sql_database" "database" {
  name     = "${var.app_name}_${var.environment}"
  instance = google_sql_database_instance.postgres.name
}

resource "google_sql_user" "user" {
  name     = "appuser"
  instance = google_sql_database_instance.postgres.name
  password = var.db_password
}
