package admin

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestHandlerServesAdminIndexAndBuiltAssets(t *testing.T) {
	handler := Handler()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("index status = %d, want %d", recorder.Code, http.StatusOK)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, "OmniRouter") {
		t.Fatalf("admin index does not contain the application title")
	}

	// A source-only build may serve the minimal fallback page. When the Vue
	// bundle is present (CI/release builds), every referenced asset must resolve.
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`).FindAllStringSubmatch(body, -1)
	for _, match := range assets {
		assetResponse := httptest.NewRecorder()
		assetRequest := httptest.NewRequest(http.MethodGet, match[1], nil)
		handler.ServeHTTP(assetResponse, assetRequest)
		if assetResponse.Code != http.StatusOK {
			t.Errorf("asset %s status = %d, want %d", match[1], assetResponse.Code, http.StatusOK)
		}
	}
}
