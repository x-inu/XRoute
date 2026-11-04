package media

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"xroute/proxy/internal/handlerutil"
)

// cliStatus is the per-tool installed/version shape the dashboard expects
// (port of each Next <tool>-settings GET, trimmed to just install + version).
type cliStatus struct {
	Installed  bool    `json:"installed"`
	Version    *string `json:"version"`
	HasXRoute bool    `json:"hasXRoute"`
}

// cliVersionTimeout bounds each `--version` probe; install detection via
// LookPath is synchronous with no timeout.
const cliVersionTimeout = 2 * time.Second

// toolDetector reports one tool's status, or err/panic → null (the reference
// all-statuses route wraps each tool GET in try/catch and maps throws to null).
type toolDetector func(context.Context) (*cliStatus, error)

// toolDef captures how each tool's presence is checked, mirroring the
// reference GET routes: mostly which <bin>; `devin` also reports version;
// `cowork` checks Claude Desktop config dirs; `copilot` has no binary check
// (reference hardcodes installed:true).
type toolDef struct {
	id         string
	bin        string
	hasVer     bool
	dirCheck   func() bool
	alwaysOn   bool
	hasXRoute func() bool
}

var cliTools = []toolDef{
	{id: "claude", bin: "claude", hasXRoute: claudeHasXRoute},
	{id: "codex", bin: "codex", hasXRoute: codexHasXRoute},
	{id: "opencode", bin: "opencode", hasXRoute: opencodeHasXRoute},
	{id: "droid", bin: "droid", hasXRoute: droidHasXRoute},
	{id: "openclaw", bin: "openclaw"},
	{id: "hermes", bin: "hermes", hasXRoute: hermesHasXRoute},
	{id: "cowork", dirCheck: coworkDirOK},
	{id: "copilot", alwaysOn: true}, // VS Code config tool; reference returns installed:true
	{id: "cline", bin: "cline"},
	{id: "kilo", bin: "kilo"},
	{id: "deepseek-tui", bin: "deepseek"},
	{id: "jcode", bin: "jcode"},
	{id: "grok-build", bin: "grok"},
	{id: "devin", bin: "devin", hasVer: true},
	{id: "pi", bin: "pi", hasXRoute: piHasXRoute},
	{id: "omp", bin: "omp", hasVer: true, hasXRoute: ompHasXRoute},
	{id: "crush", bin: "crush", hasXRoute: crushHasXRoute},
	{id: "forge", bin: "forge"},
	{id: "smelt", bin: "smelt"},
	{id: "codewhale", bin: "codewhale"},
}

// CLIToolsHandler aggregates per-tool CLI install/version status for the
// dashboard (port of the Next /cli-tools/all-statuses batch GET).
type CLIToolsHandler struct{}

// NewCLIToolsHandler returns a CLIToolsHandler. Stateless: nothing to wire up.
func NewCLIToolsHandler() *CLIToolsHandler {
	return &CLIToolsHandler{}
}

// HandleAllStatuses returns a flat {toolId: {installed, version}|null} map.
// A tool that errors or panics during detection reports null, matching the
// reference's per-tool try/catch.
func (h *CLIToolsHandler) HandleAllStatuses(w http.ResponseWriter, r *http.Request) {
	handlerutil.WriteJSON(w, http.StatusOK, cliStatuses(r.Context()))
}

// cliStatuses aggregates statuses for all known CLI tools.
func cliStatuses(ctx context.Context) map[string]*cliStatus {
	return detectAll(ctx, cliDetectors())
}

// cliDetectors builds the detection map for every known tool.
func cliDetectors() map[string]toolDetector {
	m := make(map[string]toolDetector, len(cliTools))
	for _, t := range cliTools {
		m[t.id] = t.detector()
	}
	return m
}

// detector returns the tool's status probe. Presence is checked on the user
// PATH via exec.LookPath (scope: plain PATH, no extra-bins trick).
func (t toolDef) detector() toolDetector {
	switch {
	case t.alwaysOn:
		return func(context.Context) (*cliStatus, error) {
			s := &cliStatus{Installed: true}
			if t.hasXRoute != nil {
				s.HasXRoute = t.hasXRoute()
			}
			return s, nil
		}
	case t.dirCheck != nil:
		return func(context.Context) (*cliStatus, error) {
			s := &cliStatus{Installed: t.dirCheck()}
			if s.Installed && t.hasXRoute != nil {
				s.HasXRoute = t.hasXRoute()
			}
			return s, nil
		}
	default:
		return func(ctx context.Context) (*cliStatus, error) {
			installed := false
			if _, err := exec.LookPath(t.bin); err == nil {
				installed = true
			}
			has9 := false
			if t.hasXRoute != nil {
				has9 = t.hasXRoute()
				if has9 {
					installed = true
				}
			}
			if !installed {
				// Not installed is a value, not an error (tool ≠ null).
				return &cliStatus{Installed: false}, nil
			}
			s := &cliStatus{Installed: true, HasXRoute: has9}
			if t.hasVer {
				if v, err := binVersion(ctx, t.bin); err == nil && v != "" {
					s.Version = &v
				}
			}
			return s, nil
		}
	}
}

// detectAll runs each tool's detector under a per-tool try/catch; a detector
// that errors or panics maps to null (reference all-statuses behavior).
func detectAll(ctx context.Context, det map[string]toolDetector) map[string]*cliStatus {
	out := make(map[string]*cliStatus, len(det))
	for id, d := range det {
		out[id] = runDetector(ctx, d)
	}
	return out
}

// runDetector invokes a detector and converts err/panic to null.
func runDetector(ctx context.Context, d toolDetector) (s *cliStatus) {
	defer func() {
		if recover() != nil {
			s = nil
		}
	}()
	s, err := d(ctx)
	if err != nil {
		return nil
	}
	return s
}

// binVersion reads the binary's first-line `--version` output (matches the
// reference devin route).
func binVersion(ctx context.Context, bin string) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, cliVersionTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "--version")
	var buf bytes.Buffer
	cmd.Stdout = &buf
	if err := cmd.Run(); err != nil {
		return "", err
	}
	line := strings.TrimSpace(buf.String())
	if line == "" {
		return "", nil
	}
	return strings.SplitN(line, "\n", 2)[0], nil
}

// coworkDirOK reports whether a Claude Desktop config dir exists (macOS),
// mirroring the reference cowork checkInstalled roots.
func coworkDirOK() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	base := filepath.Join(home, "Library", "Application Support")
	for _, d := range []string{"Claude-3p", "Claude"} {
		if fi, err := os.Stat(filepath.Join(base, d)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

var claudeREHasBaseURL = regexp.MustCompile(`"(ANTHROPIC_BASE_URL|NINEROUTER_URL)"\s*:\s*"[^"]+"`)

// ToolHasXRoute reports whether the named tool has xroute configured.
func ToolHasXRoute(id string) bool {
	for _, t := range cliTools {
		if t.id == id {
			if t.hasXRoute != nil {
				return t.hasXRoute()
			}
			return false
		}
	}
	return false
}

// claudeHasXRoute reports whether Claude Code is configured to use xroute.
func claudeHasXRoute() bool {
	if u := os.Getenv("ANTHROPIC_BASE_URL"); strings.TrimSpace(u) != "" {
		return true
	}
	if u := os.Getenv("NINEROUTER_URL"); strings.TrimSpace(u) != "" {
		return true
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	for _, rel := range []string{
		filepath.Join(".claude", "settings.json"),
		".claude.json",
	} {
		p := filepath.Join(home, rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if claudeConfigBytesHasXRoute(raw) {
			return true
		}
	}
	return false
}

func claudeConfigBytesHasXRoute(raw []byte) bool {
	var cfg struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(raw, &cfg) == nil && cfg.Env != nil {
		if v, ok := cfg.Env["ANTHROPIC_BASE_URL"].(string); ok && strings.TrimSpace(v) != "" {
			return true
		}
		if v, ok := cfg.Env["NINEROUTER_URL"].(string); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return claudeREHasBaseURL.Match(raw)
}

func codexHasXRoute() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		return false
	}
	s := string(raw)
	return strings.Contains(s, `model_provider = "xroute"`) || strings.Contains(s, `[model_providers.xroute]`)
}

func opencodeHasXRoute() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.json"))
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte(`"xroute"`))
}

func droidHasXRoute() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".factory", "settings.json"))
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte(`"custom:XRoute`))
}

func crushHasXRoute() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".config", "crush", "crush.json"))
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte(`"xroute"`))
}

func ompHasXRoute() bool {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(home, ".omp", "agent", "models.yml"))
	if err != nil {
		return false
	}
	return bytes.Contains(raw, []byte("xroute"))
}

func hermesHasXRoute() bool {
	home, err := resolveHermesHome("")
	if err != nil {
		return false
	}
	yaml, err := readHermesText(home.ConfigPath)
	if err != nil || yaml == "" {
		return false
	}
	return parseHermesSettings(yaml).hasXRouteConfig()
}

func piHasXRoute() bool {
	return piHasXRouteConfig(readPiConfig())
}
