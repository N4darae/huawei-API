package eventbus

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

const eventsTS = "../../web/src/api/events.ts"

func readEventsTS(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(eventsTS)
	if err != nil {
		t.Fatalf("read %s: %v (run: make gen-events)", eventsTS, err)
	}
	return string(b)
}

func block(t *testing.T, src, open, close string) string {
	t.Helper()
	i := strings.Index(src, open)
	if i < 0 {
		t.Fatalf("%s does not contain %q", eventsTS, open)
	}
	rest := src[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		t.Fatalf("%s has an unterminated %q block", eventsTS, open)
	}
	return rest[:j]
}

var tsMember = regexp.MustCompile(`'([^']+)'`)

func TestGoEventTypesMatchTypeScriptUnion(t *testing.T) {
	src := readEventsTS(t)
	found := tsMember.FindAllStringSubmatch(block(t, src, "export const EVENT_TYPES = [", "] as const"), -1)

	inTS := map[string]bool{}
	for _, m := range found {
		if inTS[m[1]] {
			t.Errorf("event type %q listed twice in events.ts", m[1])
		}
		inTS[m[1]] = true
	}

	inGo := map[string]bool{}
	for _, e := range AllEventTypes() {
		if inGo[string(e)] {
			t.Errorf("event type %q declared twice in Go", e)
		}
		inGo[string(e)] = true
	}

	for name := range inGo {
		if !inTS[name] {
			t.Errorf("Go declares event %q but events.ts does not; run make gen-events", name)
		}
	}
	for name := range inTS {
		if !inGo[name] {
			t.Errorf("events.ts declares event %q but Go does not; run make gen-events", name)
		}
	}
	if len(inGo) != len(inTS) {
		t.Fatalf("event union sizes differ: go=%d ts=%d", len(inGo), len(inTS))
	}
	if len(inGo) == 0 {
		t.Fatal("no event types found on either side")
	}
}

func TestEveryEventTypeHasAPayloadMapping(t *testing.T) {
	src := readEventsTS(t)
	body := block(t, src, "export interface EventData {", "\n}")
	for _, e := range AllEventTypes() {
		if !strings.Contains(body, "'"+string(e)+"'") && !strings.Contains(body, "\n  "+string(e)+":") {
			t.Errorf("event %q has no entry in the EventData payload map", e)
		}
	}
	topics := block(t, src, "export const EVENT_TOPIC: Record<EventType, Topic> = {", "\n}")
	for _, e := range AllEventTypes() {
		if !strings.Contains(topics, "'"+e.Topic()+"'") {
			t.Errorf("topic %q for event %q missing from EVENT_TOPIC", e.Topic(), e)
		}
	}
}

func TestTopicsMatchTypeScript(t *testing.T) {
	src := readEventsTS(t)
	found := tsMember.FindAllStringSubmatch(block(t, src, "export const TOPICS = [", "] as const"), -1)
	inTS := make([]string, 0, len(found))
	for _, m := range found {
		inTS = append(inTS, m[1])
	}
	inGo := append([]string(nil), AllTopics()...)
	sort.Strings(inTS)
	sort.Strings(inGo)
	if strings.Join(inTS, ",") != strings.Join(inGo, ",") {
		t.Fatalf("topic lists differ: go=%v ts=%v", inGo, inTS)
	}
}

func TestEnvelopeHasNoReplayFields(t *testing.T) {
	src := readEventsTS(t)
	body := block(t, src, "export interface EventEnvelope<T extends EventType = EventType> {", "\n}")
	for _, banned := range []string{" id:", " seq:", "last_event_id"} {
		if strings.Contains(body, banned) {
			t.Errorf("SSE replay was cut; envelope must not carry %q", strings.TrimSpace(banned))
		}
	}
	for _, want := range []string{"type:", "topic:", "node_id:", "subject:", "ts:", "data:"} {
		if !strings.Contains(body, want) {
			t.Errorf("envelope is missing %q", want)
		}
	}
}
