import type { components } from './schema'

export const EVENT_TYPES = [
  'hello',
  'proxy.patch',
  'dongle.patch',
  'op.update',
  'op.done',
  'sms.received',
  'system.notice',
] as const

export type EventType = (typeof EVENT_TYPES)[number]

export const TOPICS = [
  'proxies',
  'dongles',
  'operations',
  'sms',
  'system',
] as const

export type Topic = (typeof TOPICS)[number]

export const EVENT_TOPIC: Record<EventType, Topic> = {
  hello: 'system',
  'proxy.patch': 'proxies',
  'dongle.patch': 'dongles',
  'op.update': 'operations',
  'op.done': 'operations',
  'sms.received': 'sms',
  'system.notice': 'system',
}

export interface EventData {
  hello: components['schemas']['HelloEvent']
  'proxy.patch': components['schemas']['PatchEvent']
  'dongle.patch': components['schemas']['PatchEvent']
  'op.update': components['schemas']['Operation']
  'op.done': components['schemas']['Operation']
  'sms.received': components['schemas']['SmsEvent']
  'system.notice': components['schemas']['NoticeEvent']
}

export interface EventEnvelope<T extends EventType = EventType> {
  type: T
  topic: string
  node_id: string
  subject: string
  ts: number
  data: EventData[T]
}

export type AnyEvent = { [K in EventType]: EventEnvelope<K> }[EventType]

export function isEventType(v: string): v is EventType {
  return (EVENT_TYPES as readonly string[]).includes(v)
}
