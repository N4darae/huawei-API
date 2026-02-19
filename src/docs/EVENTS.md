# EVENTS

The event vocabulary is a frozen contract. Go side: `internal/eventbus/types.go`.
TypeScript side: `web/src/api/events.ts`, **generated** by `make gen-events`.
A golden test in `internal/eventbus/golden_test.go` fails the build if the two drift apart.

## Envelope

```json
{
  "type": "proxy.patch",
  "topic": "proxies",
  "node_id": "local",
  "subject": "prx_01H...",
  "ts": 1730000000000,
  "data": { }
}
```

| field | type | meaning |
|---|---|---|
| `type` | `EventType` | one of the seven values below |
| `topic` | string | subscription channel; derived from `type` |
| `node_id` | string | which node emitted it |
| `subject` | string | the id the event is about (proxy id, dongle id, operation id) |
| `ts` | integer | unix millis |
| `data` | object | payload, shape depends on `type` |

There is **no `id` and no `seq`**, and the server never honours `Last-Event-ID`. SSE replay was cut:
the client refetches its queries whenever the stream reconnects. See DECISIONS D-53.

## Types

| `type` | topic | `data` schema | emitted when |
|---|---|---|---|
| `hello` | `system` | `HelloEvent` | immediately on opening the stream; carries `node_id`, `server_time`, the subscribed `topics` and `product` |
| `proxy.patch` | `proxies` | `PatchEvent` | any observable field of a proxy changed; `fields` is a sparse patch |
| `dongle.patch` | `dongles` | `PatchEvent` | any observable field of a dongle changed |
| `op.update` | `operations` | `Operation` | an operation changed `state`, `step` or `pct` |
| `op.done` | `operations` | `Operation` | an operation reached a terminal state |
| `sms.received` | `sms` | `SmsEvent` | a new inbox message was observed |
| `system.notice` | `system` | `NoticeEvent` | operator-visible notice; `level` is `info`, `warn` or `error` |

## Topics

`proxies`, `dongles`, `operations`, `sms`, `system`. `*` subscribes to everything.
`GET /api/v1/events?topics=proxies,operations` filters server side; omitting the parameter subscribes
to everything.

## Delivery guarantees

- **Best effort, at most once.** A subscriber whose buffer fills up has its channel **closed**; the
  client must reconnect and refetch. That is deliberate: a silently dropped patch would leave the UI
  permanently wrong, while a closed stream is observable.
- Events are **not** persisted. `operations` and `rotations` in SQLite are the durable record.
- `data` for a `*.patch` event is a **sparse** object. A field that is absent did not change; it is not
  null.

## Rotation step vocabulary

`op.update` for an `OpKind` of `rotate` walks this exact sequence, which the SPA switches on:

```
precheck → fence → data_off → hold → data_on → wait_connect → unfence → verify → done
```

Internal Go state names are free; this string sequence is not.

## nginx

`/api/v1/events` requires `proxy_buffering off;` and the response carries `X-Accel-Buffering: no`.
Without both, the stream appears to hang forever and nothing is logged.

## Adding an event type

1. Add the constant to `internal/eventbus/types.go` and to `AllEventTypes()`.
2. Add the payload schema to `api/openapi.yaml` under `components.schemas`.
3. Map the type to that schema in `tools/gen/events/main.go`.
4. Run `make gen` and commit the regenerated `web/src/api/events.ts` and `schema.d.ts`.
5. Add a row to the table above.

Steps 1 and 3 without step 4 fail the golden test; step 3 is mandatory because the generator refuses to
run when an event type has no payload mapping.
