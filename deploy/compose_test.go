package deploy

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestProductionCompose(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker Compose is required to validate deployment configuration")
	}
	for key, value := range map[string]string{
		"APP_IMAGE_TAG": "ghcr.io/example/automations:test-sha",
		"APP_HOSTNAME":  "automations.example.test", "GOOGLE_CLIENT_ID": "test-client",
		"AUTH_ALLOWED_DOMAINS": "example.test", "ADMIN_EMAILS": "admin@example.test",
		"POSTGRES_PASSWORD": "test-password", "DATABASE_URL": "postgres://automations:test-password@postgres:5432/automations?sslmode=disable",
		"GOOGLE_CLIENT_SECRET": "test-secret", "CSRF_SECRET": "test-csrf",
		"ENCRYPTION_KEY": "test-key", "GOOGLE_CREDENTIALS_JSON": `{}`, "TRAEFIK_STACK": "automations-test",
	} {
		t.Setenv(key, value)
	}
	for _, key := range []string{"", "test-typesafe-key"} {
		t.Setenv("TYPESAFE_API_KEY", key)
		output, err := exec.Command("docker", "compose", "-p", "automations-config-test", "-f", "compose.portainer.yaml", "config", "--format", "json").Output()
		if err != nil {
			t.Fatal(err)
		}
		var config struct {
			Services map[string]struct {
				Image       string            `json:"image"`
				Build       any               `json:"build"`
				Ports       []any             `json:"ports"`
				Environment map[string]string `json:"environment"`
				DependsOn   map[string]struct {
					Condition string `json:"condition"`
				} `json:"depends_on"`
			} `json:"services"`
		}
		if err := json.Unmarshal(output, &config); err != nil {
			t.Fatal(err)
		}
		app, migrate := config.Services["app"], config.Services["migrate"]
		if app.Image == "" || app.Image != migrate.Image || app.DependsOn["migrate"].Condition != "service_completed_successfully" || migrate.DependsOn["postgres"].Condition != "service_healthy" {
			t.Fatal("app must use the migration image and wait for successful migrations")
		}
		if app.Environment["APP_ENV"] != "production" || app.Environment["APP_BASE_URL"] != "https://automations.example.test" || app.Environment["ENCRYPTION_KEY_FILE"] != "/run/secrets/encryption_key" || app.Environment["TYPESAFE_API_KEY"] != key {
			t.Fatal("production authentication and optional Jev configuration are inconsistent")
		}
		for name, service := range config.Services {
			if len(service.Ports) != 0 || service.Build != nil {
				t.Fatalf("%s must use published images and stay behind the reverse proxy", name)
			}
		}
	}
	for _, key := range []string{"APP_IMAGE_TAG", "APP_HOSTNAME", "GOOGLE_CLIENT_ID", "AUTH_ALLOWED_DOMAINS", "ADMIN_EMAILS"} {
		t.Run("missing "+key, func(t *testing.T) {
			t.Setenv(key, "")
			if err := exec.Command("docker", "compose", "-p", "automations-config-test", "-f", "compose.portainer.yaml", "config", "--quiet").Run(); err == nil {
				t.Fatalf("missing %s must prevent deployment", key)
			}
		})
	}
}
