import { useState } from 'react'
import { putKey, getKey, deleteKey } from '../api'
import type { LocalEvent } from '../types'

interface Props {
  leaderId: string
  onLocalEvent: (e: LocalEvent) => void
}

type Result = { type: 'ok' | 'err'; text: string } | null

export default function KVPanel({ leaderId, onLocalEvent }: Props) {
  const [key, setKey] = useState('')
  const [value, setValue] = useState('')
  const [result, setResult] = useState<Result>(null)
  const [busy, setBusy] = useState(false)

  const push = (title: string, detail?: string) =>
    onLocalEvent({ id: `${Date.now()}-${title}`, timestamp: Date.now(), kind: 'kv', title, detail })

  async function run(op: 'PUT' | 'GET' | 'DELETE') {
    if (!key) return
    setBusy(true)
    setResult(null)
    try {
      if (op === 'PUT') {
        await putKey(key, value)
        setResult({ type: 'ok', text: `PUT ${key} = ${value}` })
        push(`PUT ${key}`, `= ${value}`)
      } else if (op === 'GET') {
        const v = await getKey(key)
        setResult({ type: 'ok', text: `${key} = ${v}` })
        push(`GET ${key}`, `= ${v}`)
      } else {
        await deleteKey(key)
        setResult({ type: 'ok', text: `DELETE ${key}` })
        push(`DELETE ${key}`)
      }
    } catch (err) {
      setResult({ type: 'err', text: err instanceof Error ? err.message : String(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="p-4 space-y-3">
      <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold">
        Routed to leader: <span className="text-green-600 font-mono">{leaderId || '—'}</span>
      </div>

      <Field label="Key" value={key} onChange={setKey} placeholder="e.g. user:42" />
      <Field label="Value" value={value} onChange={setValue} placeholder="payload (PUT only)" />

      <div className="grid grid-cols-3 gap-2">
        <OpBtn disabled={!key || !value || busy} onClick={() => run('PUT')} color="green">PUT</OpBtn>
        <OpBtn disabled={!key || busy} onClick={() => run('GET')} color="blue">GET</OpBtn>
        <OpBtn disabled={!key || busy} onClick={() => run('DELETE')} color="red">DELETE</OpBtn>
      </div>

      {result && (
        <div className={`rounded-lg px-3 py-2 text-sm font-mono border slide-in ${
          result.type === 'ok'
            ? 'bg-green-50 border-green-200 text-green-800'
            : 'bg-red-50 border-red-200 text-red-700'
        }`}>
          <span className="text-[10px] uppercase tracking-wider mr-1.5 opacity-60">
            {result.type}
          </span>
          {result.text}
        </div>
      )}

      <p className="text-[11px] text-slate-400 leading-relaxed pt-1">
        If this node is not the leader, the request is transparently proxied. Check the Replication Log (left panel) to see the entry commit on every node.
      </p>
    </div>
  )
}

function Field({ label, value, onChange, placeholder }: {
  label: string; value: string; onChange: (v: string) => void; placeholder?: string
}) {
  return (
    <label className="block">
      <span className="text-[10px] uppercase tracking-widest text-slate-500 font-semibold">{label}</span>
      <input
        className="mt-1 w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono text-slate-900 bg-white placeholder-slate-400 focus:outline-none focus:border-slate-500 focus:ring-1 focus:ring-slate-200 transition"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
      />
    </label>
  )
}

function OpBtn({ color, disabled, onClick, children }: {
  color: 'green' | 'blue' | 'red'; disabled?: boolean; onClick: () => void; children: React.ReactNode
}) {
  const cls = {
    green: 'bg-green-600 hover:bg-green-700 text-white',
    blue:  'bg-blue-600  hover:bg-blue-700  text-white',
    red:   'bg-red-600   hover:bg-red-700   text-white',
  }[color]
  return (
    <button disabled={disabled} onClick={onClick}
      className={`py-2 rounded-lg text-sm font-semibold transition shadow-sm disabled:opacity-40 disabled:cursor-not-allowed ${cls}`}>
      {children}
    </button>
  )
}
