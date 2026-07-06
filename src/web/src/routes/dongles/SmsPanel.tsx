import { useState } from 'react'
import { Badge, Button, Input, Notice, Select, TextArea } from '../../design'
import type { SmsQuery } from '../../api/keys'
import { useDeleteSms, useMarkSmsRead, useSendSms, useSms } from '../../api/query'
import { formatClock } from '../proxies/format'

export const FRAGMENT_MARKER = '[fragment]'

const BOXES: Array<{ value: SmsQuery['box']; label: string }> = [
  { value: 1, label: 'Inbox' },
  { value: 2, label: 'Sent' },
  { value: 3, label: 'Drafts' },
]

export function SmsPanel({ dongleId }: { dongleId: string }) {
  const [box, setBox] = useState<1 | 2 | 3>(1)
  const [to, setTo] = useState('')
  const [body, setBody] = useState('')
  const list = useSms(dongleId, { box, page: 1, size: 50 })
  const send = useSendSms()
  const del = useDeleteSms()
  const read = useMarkSmsRead()

  const items = list.data?.items ?? []
  const fragments = items.filter((m) => m.is_fragment).length

  return (
    <div className="col" style={{ paddingTop: 12, gap: 16 }}>
      <section className="card">
        <h3 className="card-title">Send</h3>
        <form
          className="col"
          onSubmit={(e) => {
            e.preventDefault()
            const numbers = to
              .split(/[,\s]+/)
              .map((s) => s.trim())
              .filter((s) => s !== '')
            if (numbers.length === 0 || body === '') return
            send.mutate({ dongleId, to: numbers, body }, { onSuccess: () => setBody('') })
          }}
        >
          <Input
            label="To"
            value={to}
            placeholder="+841234567, +849876543"
            onChange={(e) => setTo(e.target.value)}
          />
          <TextArea
            label="Message"
            rows={3}
            value={body}
            onChange={(e) => setBody(e.target.value)}
            hint={`${body.length} characters — over 160 the modem splits it into fragments`}
          />
          {send.isError ? (
            <Notice tone="danger" title="Could not queue the message">
              {send.error.message}
            </Notice>
          ) : null}
          {send.isSuccess ? <Notice tone="ok" title="Queued on the modem" /> : null}
          <div className="row">
            <Button type="submit" variant="primary" busy={send.isPending}>
              Send SMS
            </Button>
          </div>
        </form>
      </section>

      <section className="card">
        <div className="row" style={{ alignItems: 'flex-end' }}>
          <h3 className="card-title" style={{ paddingBottom: 6 }}>
            Inbox
          </h3>
          <Select
            label="Box"
            value={String(box)}
            onChange={(e) => setBox(Number(e.target.value) as 1 | 2 | 3)}
          >
            {BOXES.map((b) => (
              <option key={String(b.value)} value={String(b.value)}>
                {b.label}
              </option>
            ))}
          </Select>
        </div>

        {fragments > 0 ? (
          <Notice tone="warn" title={`${fragments} of these are message fragments`}>
            A long SMS arrives as several parts. Every part is shown with a {FRAGMENT_MARKER} marker so
            nothing is silently dropped; read them in order.
          </Notice>
        ) : null}

        {list.isError ? (
          <Notice tone="danger" title="Could not load messages">
            {list.error.message}
          </Notice>
        ) : null}

        <ul className="list">
          {items.map((m) => (
            <li key={m.index} className="sms" data-unread={!m.read}>
              <div className="sms-head">
                <span className="mono">{m.phone}</span>
                <span>{formatClock(m.sent_at)}</span>
                {m.is_fragment ? <Badge tone="warn">{FRAGMENT_MARKER}</Badge> : null}
                {!m.read ? <Badge tone="info">unread</Badge> : null}
                {!m.read ? (
                  <Button onClick={() => read.mutate({ dongleId, index: m.index })} aria-label={`Mark ${m.index} read`}>
                    Mark read
                  </Button>
                ) : null}
                <Button
                  variant="danger"
                  onClick={() => del.mutate({ dongleId, index: m.index })}
                  aria-label={`Delete message ${m.index}`}
                >
                  Delete
                </Button>
              </div>
              <div className="sms-body">
                {m.is_fragment ? <span className="mono muted">{FRAGMENT_MARKER} </span> : null}
                {m.content}
              </div>
            </li>
          ))}
          {items.length === 0 && !list.isPending ? <li className="list-empty">No messages.</li> : null}
        </ul>
      </section>
    </div>
  )
}
