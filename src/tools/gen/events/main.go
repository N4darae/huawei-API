package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/n4darae/huawei-API/src/internal/eventbus"
)

var payloadSchema = map[eventbus.EventType]string{
	eventbus.EvHello:        "HelloEvent",
	eventbus.EvProxyPatch:   "PatchEvent",
	eventbus.EvDonglePatch:  "PatchEvent",
	eventbus.EvOpUpdate:     "Operation",
	eventbus.EvOpDone:       "Operation",
	eventbus.EvSMSReceived:  "SmsEvent",
	eventbus.EvSystemNotice: "NoticeEvent",
}

func main() {
	out := flag.String("out", "web/src/api/events.ts", "typescript event contract output")
	flag.Parse()

	types := eventbus.AllEventTypes()
	for _, t := range types {
		if _, ok := payloadSchema[t]; !ok {
			fmt.Fprintf(os.Stderr, "gen events: event type %q has no payload schema mapping\n", t)
			os.Exit(1)
		}
	}
	if len(payloadSchema) != len(types) {
		fmt.Fprintf(os.Stderr, "gen events: payload map has %d entries for %d event types\n", len(payloadSchema), len(types))
		os.Exit(1)
	}

	var b bytes.Buffer
	b.WriteString("import type { components } from './schema'\n\n")

	b.WriteString("export const EVENT_TYPES = [\n")
	for _, t := range types {
		b.WriteString("  '" + string(t) + "',\n")
	}
	b.WriteString("] as const\n\n")
	b.WriteString("export type EventType = (typeof EVENT_TYPES)[number]\n\n")

	b.WriteString("export const TOPICS = [\n")
	for _, topic := range eventbus.AllTopics() {
		b.WriteString("  '" + topic + "',\n")
	}
	b.WriteString("] as const\n\n")
	b.WriteString("export type Topic = (typeof TOPICS)[number]\n\n")

	b.WriteString("export const EVENT_TOPIC: Record<EventType, Topic> = {\n")
	for _, t := range types {
		b.WriteString("  " + key(string(t)) + ": '" + t.Topic() + "',\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("export interface EventData {\n")
	for _, t := range types {
		b.WriteString("  " + key(string(t)) + ": components['schemas']['" + payloadSchema[t] + "']\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("export interface EventEnvelope<T extends EventType = EventType> {\n")
	b.WriteString("  type: T\n")
	b.WriteString("  topic: string\n")
	b.WriteString("  node_id: string\n")
	b.WriteString("  subject: string\n")
	b.WriteString("  ts: number\n")
	b.WriteString("  data: EventData[T]\n")
	b.WriteString("}\n\n")

	b.WriteString("export type AnyEvent = { [K in EventType]: EventEnvelope<K> }[EventType]\n\n")

	b.WriteString("export function isEventType(v: string): v is EventType {\n")
	b.WriteString("  return (EVENT_TYPES as readonly string[]).includes(v)\n")
	b.WriteString("}\n")

	if err := os.WriteFile(*out, b.Bytes(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen events:", err)
		os.Exit(1)
	}
}

func key(s string) string {
	if strings.ContainsAny(s, ".-") {
		return "'" + s + "'"
	}
	return s
}
