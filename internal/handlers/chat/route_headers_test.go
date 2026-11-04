package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"xroute/proxy/internal/db"
)

func TestRouteHeaders_SingleModel(t *testing.T) {
	resetRouteHeadersConfigForTesting()
	os.Unsetenv("NINEROUTER_ROUTE_HEADERS")
	rec := httptest.NewRecorder()
	injectRouteHeaders(rec, "anthropic", "claude-3.5-sonnet", "", 1)

	resp := rec.Result()
	if resp.Header.Get("x-xroute-provider") != "anthropic" {
		t.Errorf("expected x-xroute-provider=anthropic, got %s", resp.Header.Get("x-xroute-provider"))
	}
	if resp.Header.Get("x-xroute-model") != "claude-3.5-sonnet" {
		t.Errorf("expected x-xroute-model=claude-3.5-sonnet, got %s", resp.Header.Get("x-xroute-model"))
	}
	if resp.Header.Get("x-xroute-combo") != "" {
		t.Errorf("expected x-xroute-combo to be empty, got %s", resp.Header.Get("x-xroute-combo"))
	}
	if resp.Header.Get("x-xroute-attempts") != "1" {
		t.Errorf("expected x-xroute-attempts=1, got %s", resp.Header.Get("x-xroute-attempts"))
	}
}

func TestRouteHeaders_ComboAndAttempts(t *testing.T) {
	resetRouteHeadersConfigForTesting()
	os.Unsetenv("NINEROUTER_ROUTE_HEADERS")
	rec := httptest.NewRecorder()
	injectRouteHeaders(rec, "openai", "gpt-4o", "my-combo", 3)

	resp := rec.Result()
	if resp.Header.Get("x-xroute-provider") != "openai" {
		t.Errorf("expected x-xroute-provider=openai, got %s", resp.Header.Get("x-xroute-provider"))
	}
	if resp.Header.Get("x-xroute-model") != "gpt-4o" {
		t.Errorf("expected x-xroute-model=gpt-4o, got %s", resp.Header.Get("x-xroute-model"))
	}
	if resp.Header.Get("x-xroute-combo") != "my-combo" {
		t.Errorf("expected x-xroute-combo=my-combo, got %s", resp.Header.Get("x-xroute-combo"))
	}
	if resp.Header.Get("x-xroute-attempts") != "3" {
		t.Errorf("expected x-xroute-attempts=3, got %s", resp.Header.Get("x-xroute-attempts"))
	}
}

func TestRouteHeaders_Disabled(t *testing.T) {
	resetRouteHeadersConfigForTesting()
	t.Setenv("NINEROUTER_ROUTE_HEADERS", "0")
	rec := httptest.NewRecorder()
	injectRouteHeaders(rec, "anthropic", "claude-3", "combo", 1)

	resp := rec.Result()
	if resp.Header.Get("x-xroute-provider") != "" {
		t.Errorf("expected headers suppressed when disabled, got x-xroute-provider=%s", resp.Header.Get("x-xroute-provider"))
	}

	resetRouteHeadersConfigForTesting()
	t.Setenv("NINEROUTER_ROUTE_HEADERS", "false")
	rec2 := httptest.NewRecorder()
	injectRouteHeaders(rec2, "anthropic", "claude-3", "combo", 1)
	resp2 := rec2.Result()
	if resp2.Header.Get("x-xroute-provider") != "" {
		t.Errorf("expected headers suppressed when NINEROUTER_ROUTE_HEADERS=false")
	}
}

func TestRouteHeaders_ClearOnFailure(t *testing.T) {
	resetRouteHeadersConfigForTesting()
	os.Unsetenv("NINEROUTER_ROUTE_HEADERS")
	rec := httptest.NewRecorder()
	injectRouteHeaders(rec, "deepseek", "deepseek-chat", "combo-a", 2)

	if rec.Header().Get("x-xroute-provider") != "deepseek" {
		t.Fatalf("expected provider header before clear")
	}

	clearRouteHeaders(rec)
	resp := rec.Result()
	if resp.Header.Get("x-xroute-provider") != "" {
		t.Errorf("expected x-xroute-provider cleared, got %s", resp.Header.Get("x-xroute-provider"))
	}
	if resp.Header.Get("x-xroute-model") != "" {
		t.Errorf("expected x-xroute-model cleared, got %s", resp.Header.Get("x-xroute-model"))
	}
	if resp.Header.Get("x-xroute-combo") != "" {
		t.Errorf("expected x-xroute-combo cleared, got %s", resp.Header.Get("x-xroute-combo"))
	}
	if resp.Header.Get("x-xroute-attempts") != "" {
		t.Errorf("expected x-xroute-attempts cleared, got %s", resp.Header.Get("x-xroute-attempts"))
	}
}

func TestRouteHeaders_DispatchIntegration(t *testing.T) {
	resetRouteHeadersConfigForTesting()
	os.Unsetenv("NINEROUTER_ROUTE_HEADERS")

	database, cleanup := setupChatTestDB(t)
	defer cleanup()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer upstream.Close()

	repo := db.NewRepo(database)
	h := NewChatHandler(repo)
	rec := httptest.NewRecorder()
	ctx := context.Background()

	err := h.tryForwardWithConnection(forwardRequestParams{
		Ctx:          ctx,
		W:            rec,
		Provider:     "openai",
		Model:        "gpt-4o",
		ComboName:    "test-combo",
		Attempts:     2,
		StartedAt:    time.Now(),
		ConnectionID: "conn-test",
		ConnData:     &ConnectionData{APIKey: "sk-test", BaseURL: upstream.URL},
		Body:         []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`),
		IsStream:     false,
		Endpoint:     "/v1/chat/completions",
	})
	if err != nil {
		t.Fatalf("tryForwardWithConnection failed: %v", err)
	}

	resp := rec.Result()
	if got := resp.Header.Get("x-xroute-provider"); got != "openai" {
		t.Errorf("expected x-xroute-provider=openai, got %q", got)
	}
	if got := resp.Header.Get("x-xroute-model"); got != "gpt-4o" {
		t.Errorf("expected x-xroute-model=gpt-4o, got %q", got)
	}
	if got := resp.Header.Get("x-xroute-combo"); got != "test-combo" {
		t.Errorf("expected x-xroute-combo=test-combo, got %q", got)
	}
	if got := resp.Header.Get("x-xroute-attempts"); got != "2" {
		t.Errorf("expected x-xroute-attempts=2, got %q", got)
	}
}
