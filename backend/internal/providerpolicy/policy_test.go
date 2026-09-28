package providerpolicy

import (
	"os"
	"strings"
	"testing"
)

// Both Nginx policies must approve the same reviewed hosts as preview does.
func TestDeployedImagePolicyMatchesReviewedDomains(t *testing.T) {
	for _, path := range []string{"../../../frontend/nginx.conf", "../../../deploy/nginx/worknet.team.conf"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		config := string(data)
		for _, host := range Defaults().Images {
			if !strings.Contains(config, "https://"+host) {
				t.Errorf("%s misses image host %s", path, host)
			}
		}
		if strings.Contains(config, "img-src https:") || strings.Contains(config, "https://*;") {
			t.Errorf("%s has an unrestricted image policy", path)
		}
	}
}

func TestInternalNamespaceRemainsBehindBasicAuth(t *testing.T) {
	data, err := os.ReadFile("../../../deploy/nginx/worknet.team.conf")
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	for _, location := range []string{"location = /internal {", "location ^~ /internal/ {", "location = /api/v1/internal {", "location ^~ /api/v1/internal/ {"} {
		start := strings.Index(config, location)
		if start < 0 {
			t.Fatalf("missing namespace guard %s", location)
		}
		block := config[start:]
		end := strings.Index(block, "\n    }")
		if end < 0 {
			t.Fatal("unterminated location")
		}
		block = block[:end]
		if !strings.Contains(block, "auth_basic_user_file /etc/nginx/auth/worknet-analytics.htpasswd;") || !strings.Contains(block, "auth_basic \"") {
			t.Errorf("unguarded %s", location)
		}
		if !strings.Contains(block, "proxy_pass http://127.0.0.1:") {
			t.Errorf("unexpected upstream for %s", location)
		}
	}
}
