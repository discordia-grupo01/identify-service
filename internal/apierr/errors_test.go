package apierr_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/discordia-grupo01/identify-service/internal/apierr"
	"github.com/gin-gonic/gin"
)

var codePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// assertEnvelope verifica la forma completa del sobre y devuelve el error.
func assertEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode string) map[string]any {
	t.Helper()
	if recorder.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, wantStatus, recorder.Body.String())
	}
	if ct := recorder.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &top); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if len(top) != 1 || top["error"] == nil {
		t.Fatalf("top-level keys = %v, want only error", top)
	}
	var body map[string]any
	if err := json.Unmarshal(top["error"], &body); err != nil {
		t.Fatalf("error is not an object: %v", err)
	}
	code, ok := body["code"].(string)
	if !ok || !codePattern.MatchString(code) {
		t.Fatalf("code = %v, want SCREAMING_SNAKE string", body["code"])
	}
	if code != wantCode {
		t.Fatalf("code = %q, want %q", code, wantCode)
	}
	if msg, ok := body["message"].(string); !ok || msg == "" {
		t.Fatalf("message = %v, want a non-empty string", body["message"])
	}
	if _, ok := body["details"].(map[string]any); !ok {
		t.Fatalf("details = %v (%T), want an object", body["details"], body["details"])
	}
	return body["details"].(map[string]any)
}

func newRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	apierr.RegisterFallbacks(router)
	// Gin 1.10 entra en pánico con HandleMethodNotAllowed si no hay ninguna ruta.
	router.GET("/ping", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return router
}

func TestAbortNormalizesNilDetailsToEmptyObject(t *testing.T) {
	router := newRouter()
	router.GET("/x", func(c *gin.Context) { apierr.Abort(c, http.StatusConflict, "SOME_CODE", "mensaje", nil) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/x", nil))

	details := assertEnvelope(t, recorder, http.StatusConflict, "SOME_CODE")
	if len(details) != 0 {
		t.Fatalf("details = %v, want {}", details)
	}
	if !strings.Contains(recorder.Body.String(), `"details":{}`) {
		t.Fatalf("details must be serialized as {}: %s", recorder.Body.String())
	}
}

func TestUnknownRouteReturnsNotFoundEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	newRouter().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/no-existe", nil))
	assertEnvelope(t, recorder, http.StatusNotFound, "NOT_FOUND")
}

func TestWrongMethodReturnsMethodNotAllowedEnvelope(t *testing.T) {
	router := newRouter()
	router.POST("/v1/login", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/login", nil))
	assertEnvelope(t, recorder, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED")
}

func TestBindingErrorsNeverLeakLibraryText(t *testing.T) {
	type req struct {
		IDToken string `json:"id_token" binding:"required"`
		Email   string `json:"email" binding:"omitempty,email"`
		Age     int    `json:"age"`
	}
	cases := []struct {
		name      string
		body      string
		wantField any
		wantReson any
	}{
		{"campo faltante", `{}`, "id_token", "required"},
		{"email inválido", `{"id_token":"a","email":"x"}`, "email", "invalid_email"},
		{"tipo inválido", `{"id_token":"a","age":"diez"}`, "age", "invalid_type"},
		{"json mal formado", `not json`, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newRouter()
			router.POST("/x", func(c *gin.Context) {
				var r req
				if err := c.ShouldBindJSON(&r); err != nil {
					apierr.InvalidBodyFromBinding(c, "Petición inválida", err)
					return
				}
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(tc.body)))

			details := assertEnvelope(t, recorder, http.StatusBadRequest, "INVALID_BODY")
			if details["field"] != tc.wantField || details["reason"] != tc.wantReson {
				t.Fatalf("details = %v, want field=%v reason=%v", details, tc.wantField, tc.wantReson)
			}
			for _, leak := range []string{"Key:", "Error:", "validator", "json:", "EOF", "invalid character"} {
				if strings.Contains(recorder.Body.String(), leak) {
					t.Fatalf("body leaks library text %q: %s", leak, recorder.Body.String())
				}
			}
		})
	}
}

func TestUnauthorizedReturnsEnvelope(t *testing.T) {
	router := newRouter()
	router.GET("/x", apierr.Unauthorized)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/x", nil))
	assertEnvelope(t, recorder, http.StatusUnauthorized, "UNAUTHORIZED")
}
