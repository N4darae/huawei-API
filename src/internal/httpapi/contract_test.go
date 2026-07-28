package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

type specRoute struct {
	Method      string
	Path        string
	OperationID string
}

func specPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("resolve spec: %v", err)
	}
	return p
}

func readSpec(t *testing.T) []specRoute {
	t.Helper()

	raw, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			OperationID string `yaml:"operationId"`
		} `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	verbs := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	out := []specRoute{}
	for path, item := range doc.Paths {
		for verb, op := range item {
			if !verbs[verb] {
				continue
			}
			out = append(out, specRoute{
				Method:      strings.ToUpper(verb),
				Path:        path,
				OperationID: op.OperationID,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Method < out[j].Method
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func mountedRoutes(t *testing.T, api *API) map[string]bool {
	t.Helper()

	r := chi.NewRouter()
	api.Mount(r)

	out := map[string]bool{}
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			route = "/"
		}
		out[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	return out
}

func TestEveryContractRouteIsMounted(t *testing.T) {
	h := newHarness(t)
	mounted := mountedRoutes(t, h.api)

	mountedByP0Router := map[string]bool{"GET /api/v1/healthz": true}

	var missing []string
	for _, route := range readSpec(t) {
		key := route.Method + " " + route.Path
		if mountedByP0Router[key] {
			continue
		}
		if !mounted[key] {
			missing = append(missing, key+"  ("+route.OperationID+")")
		}
	}
	if len(missing) > 0 {
		t.Fatalf("the contract declares routes this module does not serve:\n  %s", strings.Join(missing, "\n  "))
	}
}

func TestNoRouteIsServedThatTheContractDoesNotDeclare(t *testing.T) {
	h := newHarness(t)

	declared := map[string]bool{}
	for _, route := range readSpec(t) {
		declared[route.Method+" "+route.Path] = true
	}

	var extra []string
	for key := range mountedRoutes(t, h.api) {
		if !declared[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	if len(extra) > 0 {
		t.Fatalf("this module serves routes the contract does not declare:\n  %s", strings.Join(extra, "\n  "))
	}
}

func TestEveryContractOperationIdIsUnique(t *testing.T) {
	seen := map[string]string{}
	for _, route := range readSpec(t) {
		if route.OperationID == "" {
			t.Errorf("%s %s has no operationId", route.Method, route.Path)
			continue
		}
		if prev, dup := seen[route.OperationID]; dup {
			t.Errorf("operationId %q is used by %s and %s %s", route.OperationID, prev, route.Method, route.Path)
		}
		seen[route.OperationID] = route.Method + " " + route.Path
	}
}

func TestEveryErrorCodeWeEmitIsInTheContractEnum(t *testing.T) {
	raw, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas struct {
				ErrorCode struct {
					Enum []string `yaml:"enum"`
				} `yaml:"ErrorCode"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	declared := map[string]bool{}
	for _, code := range doc.Components.Schemas.ErrorCode.Enum {
		declared[code] = true
	}
	if len(declared) == 0 {
		t.Fatal("the contract declares no ErrorCode enum")
	}
	for _, code := range AllErrorCodes() {
		if !declared[code] {
			t.Errorf("the server emits %q but the contract does not enumerate it", code)
		}
	}
}

func TestContractStepEnumCoversEveryStepWeEmit(t *testing.T) {
	raw, err := os.ReadFile(specPath(t))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc struct {
		Components struct {
			Schemas struct {
				OpStep struct {
					Enum []string `yaml:"enum"`
				} `yaml:"OpStep"`
			} `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	declared := map[string]bool{}
	for _, s := range doc.Components.Schemas.OpStep.Enum {
		declared[s] = true
	}
	for _, s := range allEmittedSteps() {
		if !declared[s] {
			t.Errorf("the server emits step %q but the contract does not enumerate it", s)
		}
	}
}
