package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

type generator struct {
	doc *yaml.Node
	buf bytes.Buffer
}

func main() {
	in := flag.String("in", "api/openapi.yaml", "openapi document")
	out := flag.String("out", "web/src/api/schema.d.ts", "typescript declaration output")
	flag.Parse()

	raw, err := os.ReadFile(*in)
	if err != nil {
		fail(err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		fail(err)
	}
	if len(root.Content) != 1 {
		fail(fmt.Errorf("%s: expected a single yaml document", *in))
	}

	g := &generator{doc: root.Content[0]}
	if err := g.run(); err != nil {
		fail(err)
	}
	if err := os.WriteFile(*out, g.buf.Bytes(), 0o644); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "gen openapi:", err)
	os.Exit(1)
}

func (g *generator) run() error {
	if err := g.paths(); err != nil {
		return err
	}
	if err := g.components(); err != nil {
		return err
	}
	return g.operations()
}

func (g *generator) paths() error {
	paths := mapVal(g.doc, "paths")
	if paths == nil {
		return fmt.Errorf("document has no paths")
	}
	g.line(0, "export interface paths {")
	for i := 0; i+1 < len(paths.Content); i += 2 {
		route := paths.Content[i].Value
		item := paths.Content[i+1]
		g.line(1, quote(route)+": {")
		for _, m := range httpMethods {
			op := mapVal(item, m)
			if op == nil {
				continue
			}
			id := scalar(mapVal(op, "operationId"))
			if id == "" {
				return fmt.Errorf("%s %s: operationId is required", strings.ToUpper(m), route)
			}
			g.line(2, m+": operations["+quote(id)+"];")
		}
		g.line(1, "};")
	}
	g.line(0, "}")
	g.blank()
	g.line(0, "export type webhooks = Record<string, never>;")
	g.blank()
	return nil
}

func (g *generator) components() error {
	schemas := mapVal(mapVal(g.doc, "components"), "schemas")
	if schemas == nil {
		return fmt.Errorf("document has no components.schemas")
	}
	g.line(0, "export interface components {")
	g.line(1, "schemas: {")
	for i := 0; i+1 < len(schemas.Content); i += 2 {
		name := schemas.Content[i].Value
		t, err := g.tsType(schemas.Content[i+1], 2)
		if err != nil {
			return fmt.Errorf("schema %s: %w", name, err)
		}
		g.line(2, name+": "+t+";")
	}
	g.line(1, "};")
	g.line(0, "}")
	g.blank()
	g.line(0, "export type $defs = Record<string, never>;")
	g.blank()
	return nil
}

func (g *generator) operations() error {
	paths := mapVal(g.doc, "paths")
	type entry struct {
		id    string
		route string
		verb  string
		node  *yaml.Node
	}
	var ops []entry
	for i := 0; i+1 < len(paths.Content); i += 2 {
		route := paths.Content[i].Value
		item := paths.Content[i+1]
		for _, m := range httpMethods {
			op := mapVal(item, m)
			if op == nil {
				continue
			}
			ops = append(ops, entry{id: scalar(mapVal(op, "operationId")), route: route, verb: m, node: op})
		}
	}
	sort.SliceStable(ops, func(i, j int) bool { return ops[i].id < ops[j].id })

	seen := map[string]string{}
	g.line(0, "export interface operations {")
	for _, op := range ops {
		if prev, dup := seen[op.id]; dup {
			return fmt.Errorf("operationId %q used by both %s and %s %s", op.id, prev, strings.ToUpper(op.verb), op.route)
		}
		seen[op.id] = strings.ToUpper(op.verb) + " " + op.route

		g.line(1, op.id+": {")
		if err := g.parameters(op.node); err != nil {
			return fmt.Errorf("%s: %w", op.id, err)
		}
		if err := g.requestBody(op.node); err != nil {
			return fmt.Errorf("%s: %w", op.id, err)
		}
		if err := g.responses(op.node); err != nil {
			return fmt.Errorf("%s: %w", op.id, err)
		}
		g.line(1, "};")
	}
	g.line(0, "}")
	return nil
}

func (g *generator) parameters(op *yaml.Node) error {
	params := mapVal(op, "parameters")
	byLoc := map[string][]*yaml.Node{}
	if params != nil {
		for _, p := range params.Content {
			resolved, err := g.deref(p)
			if err != nil {
				return err
			}
			in := scalar(mapVal(resolved, "in"))
			byLoc[in] = append(byLoc[in], resolved)
		}
	}
	if len(byLoc) == 0 {
		g.line(2, "parameters?: never;")
		return nil
	}
	g.line(2, "parameters: {")
	for _, loc := range []string{"path", "query", "header", "cookie"} {
		list := byLoc[loc]
		if len(list) == 0 {
			continue
		}
		optional := ""
		if !anyRequired(list) {
			optional = "?"
		}
		g.line(3, loc+optional+": {")
		for _, p := range list {
			name := scalar(mapVal(p, "name"))
			t, err := g.tsType(mapVal(p, "schema"), 4)
			if err != nil {
				return err
			}
			opt := "?"
			if scalar(mapVal(p, "required")) == "true" {
				opt = ""
			}
			g.line(4, quote(name)+opt+": "+t+";")
		}
		g.line(3, "};")
	}
	g.line(2, "};")
	return nil
}

func anyRequired(list []*yaml.Node) bool {
	for _, p := range list {
		if scalar(mapVal(p, "required")) == "true" {
			return true
		}
	}
	return false
}

func (g *generator) requestBody(op *yaml.Node) error {
	body := mapVal(op, "requestBody")
	if body == nil {
		g.line(2, "requestBody?: never;")
		return nil
	}
	resolved, err := g.deref(body)
	if err != nil {
		return err
	}
	content := mapVal(resolved, "content")
	if content == nil {
		g.line(2, "requestBody?: never;")
		return nil
	}
	opt := "?"
	if scalar(mapVal(resolved, "required")) == "true" {
		opt = ""
	}
	g.line(2, "requestBody"+opt+": {")
	g.line(3, "content: {")
	if err := g.contentEntries(content, 4); err != nil {
		return err
	}
	g.line(3, "};")
	g.line(2, "};")
	return nil
}

func (g *generator) responses(op *yaml.Node) error {
	responses := mapVal(op, "responses")
	if responses == nil {
		return fmt.Errorf("responses are required")
	}
	g.line(2, "responses: {")
	for i := 0; i+1 < len(responses.Content); i += 2 {
		code := responses.Content[i].Value
		resolved, err := g.deref(responses.Content[i+1])
		if err != nil {
			return err
		}
		key := code
		if _, err := strconv.Atoi(code); err != nil {
			key = quote(code)
		}
		content := mapVal(resolved, "content")
		if content == nil {
			g.line(3, key+": {")
			g.line(4, "content?: never;")
			g.line(3, "};")
			continue
		}
		g.line(3, key+": {")
		g.line(4, "content: {")
		if err := g.contentEntries(content, 5); err != nil {
			return err
		}
		g.line(4, "};")
		g.line(3, "};")
	}
	g.line(2, "};")
	return nil
}

func (g *generator) contentEntries(content *yaml.Node, indent int) error {
	for i := 0; i+1 < len(content.Content); i += 2 {
		media := content.Content[i].Value
		t, err := g.tsType(mapVal(content.Content[i+1], "schema"), indent)
		if err != nil {
			return err
		}
		g.line(indent, quote(media)+": "+t+";")
	}
	return nil
}

func (g *generator) tsType(n *yaml.Node, indent int) (string, error) {
	if n == nil {
		return "unknown", nil
	}
	if ref := scalar(mapVal(n, "$ref")); ref != "" {
		name, ok := schemaRefName(ref)
		if !ok {
			return "", fmt.Errorf("unsupported $ref %q", ref)
		}
		return "components[\"schemas\"][" + quote(name) + "]", nil
	}

	nullable := scalar(mapVal(n, "nullable")) == "true"
	base, err := g.tsBase(n, indent)
	if err != nil {
		return "", err
	}
	if nullable {
		base = "(" + base + ") | null"
	}
	return base, nil
}

func (g *generator) tsBase(n *yaml.Node, indent int) (string, error) {
	kind := scalar(mapVal(n, "type"))

	if enum := mapVal(n, "enum"); enum != nil {
		parts := make([]string, 0, len(enum.Content))
		for _, e := range enum.Content {
			if kind == "integer" || kind == "number" {
				parts = append(parts, e.Value)
			} else {
				parts = append(parts, quote(e.Value))
			}
		}
		return strings.Join(parts, " | "), nil
	}

	switch kind {
	case "string":
		return "string", nil
	case "integer", "number":
		return "number", nil
	case "boolean":
		return "boolean", nil
	case "array":
		item, err := g.tsType(mapVal(n, "items"), indent)
		if err != nil {
			return "", err
		}
		if strings.ContainsAny(item, "{|") {
			return "(" + item + ")[]", nil
		}
		return item + "[]", nil
	case "object", "":
		return g.tsObject(n, indent)
	default:
		return "", fmt.Errorf("unsupported schema type %q", kind)
	}
}

func (g *generator) tsObject(n *yaml.Node, indent int) (string, error) {
	props := mapVal(n, "properties")
	additional := mapVal(n, "additionalProperties")

	if props == nil {
		if additional == nil {
			return "unknown", nil
		}
		if additional.Kind == yaml.ScalarNode {
			if additional.Value == "true" {
				return "{ [key: string]: unknown }", nil
			}
			return "Record<string, never>", nil
		}
		t, err := g.tsType(additional, indent)
		if err != nil {
			return "", err
		}
		return "{ [key: string]: " + t + " }", nil
	}

	required := map[string]bool{}
	if req := mapVal(n, "required"); req != nil {
		for _, r := range req.Content {
			required[r.Value] = true
		}
	}

	var b strings.Builder
	b.WriteString("{\n")
	for i := 0; i+1 < len(props.Content); i += 2 {
		name := props.Content[i].Value
		t, err := g.tsType(props.Content[i+1], indent+1)
		if err != nil {
			return "", fmt.Errorf("property %s: %w", name, err)
		}
		opt := "?"
		if required[name] {
			opt = ""
		}
		b.WriteString(pad(indent+1) + quote(name) + opt + ": " + t + ";\n")
	}
	if additional != nil && additional.Kind == yaml.ScalarNode && additional.Value == "true" {
		b.WriteString(pad(indent+1) + "[key: string]: unknown;\n")
	}
	b.WriteString(pad(indent) + "}")
	return b.String(), nil
}

func (g *generator) deref(n *yaml.Node) (*yaml.Node, error) {
	ref := scalar(mapVal(n, "$ref"))
	if ref == "" {
		return n, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("unsupported $ref %q", ref)
	}
	cur := g.doc
	for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = mapVal(cur, seg)
		if cur == nil {
			return nil, fmt.Errorf("dangling $ref %q", ref)
		}
	}
	return cur, nil
}

func schemaRefName(ref string) (string, bool) {
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	return strings.TrimPrefix(ref, prefix), true
}

func mapVal(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func pad(indent int) string { return strings.Repeat("  ", indent) }

func (g *generator) line(indent int, s string) {
	g.buf.WriteString(pad(indent))
	g.buf.WriteString(s)
	g.buf.WriteByte('\n')
}

func (g *generator) blank() { g.buf.WriteByte('\n') }
