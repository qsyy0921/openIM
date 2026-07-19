package observe

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPMetricsUsesStablePatternAndFirstStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /resources/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
		w.WriteHeader(http.StatusInternalServerError)
	})
	metric := httpRequests.WithLabelValues("GET /resources/{id}", http.MethodGet, "204")
	before := testutil.ToFloat64(metric)

	response := httptest.NewRecorder()
	HTTPMetrics(mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/resources/private-id", nil))

	if response.Code != http.StatusNoContent {
		t.Fatalf("response status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := testutil.ToFloat64(metric); got != before+1 {
		t.Fatalf("metric count = %v, want %v", got, before+1)
	}
}
