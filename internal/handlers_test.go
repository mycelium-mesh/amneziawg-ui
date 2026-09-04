package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// newTestAPI wires the real handlers onto a Fiber app backed by a manager
// whose state lives in a temp dir.
func newTestAPI(t *testing.T) (*fiber.App, *Manager) {
	t.Helper()
	m := newClientManager(t)
	app := fiber.New()
	NewHandlers(m, nil).RegisterRoutes(app)
	return app, m
}

func call(t *testing.T, app *fiber.App, method, path, body string) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

// "No such server" and "the interface refused to come up" used to be the same
// answer: 404 with "server not found or failed to start".
func TestMissingServerIs404AndAFailedStartIs500(t *testing.T) {
	app, _ := newTestAPI(t)

	status, body := call(t, app, http.MethodPost, "/api/servers/nope/start", "")
	if status != http.StatusNotFound {
		t.Errorf("unknown server: status = %d, want 404 (%s)", status, body)
	}

	// s1 exists; awg-quick is not installed here, so bringing it up fails.
	status, body = call(t, app, http.MethodPost, "/api/servers/s1/start", "")
	if status != http.StatusInternalServerError {
		t.Errorf("failed start: status = %d, want 500 (%s)", status, body)
	}
	var errResp ErrorResponse
	if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error == "" {
		t.Errorf("error payload = %s (%v)", body, err)
	}
}

func TestMalformedBodyIsRejected(t *testing.T) {
	app, m := newTestAPI(t)
	client, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		"/api/servers/s1/clients",
		"/api/servers/s1/clients/" + client.ID + "/allowed-ips",
	} {
		method := http.MethodPost
		if strings.HasSuffix(path, "allowed-ips") {
			method = http.MethodPut
		}
		status, body := call(t, app, method, path, "{not json")
		if status != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400 (%s)", method, path, status, body)
		}
	}

	// A body that parses but leaves everything at its default is still fine.
	status, _ := call(t, app, http.MethodPut,
		"/api/servers/s1/clients/"+client.ID+"/allowed-ips", `{}`)
	if status != http.StatusOK {
		t.Errorf("empty object: status = %d, want 200", status)
	}
}

func TestActivatingAnActiveClientIsAConflict(t *testing.T) {
	app, m := newTestAPI(t)
	client, _, err := m.AddClient("s1", "alice", false, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	status, body := call(t, app, http.MethodPost,
		"/api/servers/s1/clients/"+client.ID+"/activate", "")
	if status != http.StatusConflict {
		t.Errorf("status = %d, want 409 (%s)", status, body)
	}
}

// The frontend parses these payloads into the types in web-ui/api; the
// backend must produce exactly those shapes.
func TestResponsesMatchTheDeclaredPayloads(t *testing.T) {
	app, m := newTestAPI(t)
	client, _, err := m.AddClient("s1", "alice", true, nil, "")
	if err != nil {
		t.Fatal(err)
	}

	status, body := call(t, app, http.MethodGet, "/api/servers/s1/info", "")
	if status != http.StatusOK {
		t.Fatalf("info: status = %d (%s)", status, body)
	}
	var info ServerInfo
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	if info.ID != "s1" || info.ClientsCount != 1 || len(info.Clients) != 1 {
		t.Errorf("ServerInfo = %+v", info)
	}
	if info.DefaultISettings["i1"] != DefaultI1 {
		t.Errorf("default I-settings missing from ServerInfo")
	}
	if info.Status != "stopped" {
		t.Errorf("status = %q, want the observed stopped", info.Status)
	}

	status, body = call(t, app, http.MethodGet,
		"/api/servers/s1/clients/"+client.ID+"/config-both", "")
	if status != http.StatusOK {
		t.Fatalf("config-both: status = %d (%s)", status, body)
	}
	var configs ClientConfigs
	if err := json.Unmarshal(body, &configs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configs.CleanConfig, "[Interface]") || configs.FullLength == 0 {
		t.Errorf("ClientConfigs = %+v", configs)
	}

	status, body = call(t, app, http.MethodDelete, "/api/servers/s1/clients/"+client.ID, "")
	if status != http.StatusOK {
		t.Fatalf("delete: status = %d (%s)", status, body)
	}
	var action ActionResult
	if err := json.Unmarshal(body, &action); err != nil {
		t.Fatal(err)
	}
	if action.Status != "deleted" || action.ClientID != client.ID {
		t.Errorf("ActionResult = %+v", action)
	}

	status, body = call(t, app, http.MethodGet, "/api/system/status", "")
	if status != http.StatusOK {
		t.Fatalf("system status: status = %d (%s)", status, body)
	}
	var sys SystemStatus
	if err := json.Unmarshal(body, &sys); err != nil {
		t.Fatal(err)
	}
	if sys.TotalServers != 1 || sys.TotalClients != 0 {
		t.Errorf("SystemStatus = %+v", sys)
	}
}

// Starting a server whose interface is already up is a conflict, not a
// failure: awg-quick would just error out with "File exists".
func TestStartingARunningServerIsAConflict(t *testing.T) {
	app, m := newTestAPI(t)
	m.noteServerStatus(m.Config.Servers[0].Interface, "running")

	status, body := call(t, app, http.MethodPost, "/api/servers/s1/start", "")
	if status != http.StatusConflict {
		t.Errorf("start: status = %d, want 409 (%s)", status, body)
	}

	m.noteServerStatus(m.Config.Servers[0].Interface, "stopped")
	status, body = call(t, app, http.MethodPost, "/api/servers/s1/stop", "")
	if status != http.StatusConflict {
		t.Errorf("stop: status = %d, want 409 (%s)", status, body)
	}
}
