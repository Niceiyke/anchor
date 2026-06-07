package control

import "testing"

func TestValidComposePath(t *testing.T) {
	ok := []string{"", "docker-compose.yml", "deploy/compose.prod.yml", "a/b/c/compose.yaml", "./compose.yml"}
	bad := []string{"/etc/passwd", "../secret.yml", "../../x", "deploy/../../etc/host"}
	for _, p := range ok {
		if !validComposePath(p) {
			t.Errorf("expected %q to be valid", p)
		}
	}
	for _, p := range bad {
		if validComposePath(p) {
			t.Errorf("expected %q to be rejected", p)
		}
	}
}

func TestIsComposeFile(t *testing.T) {
	yes := []string{"docker-compose.yml", "docker-compose.prod.yaml", "compose.yml", "compose.override.yaml", "deploy/docker-compose.staging.yml"}
	no := []string{"Dockerfile", "README.md", "config.yml", "k8s/deployment.yaml", "compose.txt"}
	for _, p := range yes {
		if !isComposeFile(p) {
			t.Errorf("expected %q to match", p)
		}
	}
	for _, p := range no {
		if isComposeFile(p) {
			t.Errorf("expected %q not to match", p)
		}
	}
}
