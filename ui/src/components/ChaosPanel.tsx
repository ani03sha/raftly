import { useEffect, useState } from 'react'
import type { ChaosState, ClusterStatus, LocalEvent } from '../types'
import { getChaosState, healAll, injectDelay, injectLoss, isolateNode, partition } from '../api'

interface Props {
  cluster: ClusterStatus | null
  onLocalEvent: (e: LocalEvent) => void
}

type Tab = 'isolate' | 'partition' | 'delay' | 'loss'

export default function ChaosPanel({ cluster, onLocalEvent }: Props) {
  const [tab, setTab] = useState<Tab>('isolate')
  const [target, setTarget] = useState('')
  const [partner, setPartner] = useState('')
  const [delayMs, setDelayMs] = useState(200)
  const [jitterMs, setJitterMs] = useState(50)
  const [lossRate, setLossRate] = useState(0.3)
  const [busy, setBusy] = useState(false)
  const [state, setState] = useState<ChaosState | null>(null)

  const nodes = cluster?.nodes.map((n) => n.id) ?? []

  useEffect(() => {
    if (!target && nodes.length > 0) setTarget(nodes[0])
    if (!partner && nodes.length > 1) setPartner(nodes[1])
  }, [nodes.length]) // eslint-disable-line

  useEffect(() => {
    const load = () => getChaosState().then(setState).catch(() => {})
    load()
    const iv = setInterval(load, 2000)
    return () => clearInterval(iv)
  }, [])

  async function act(label: string, detail: string, fn: () => Promise<void>) {
    setBusy(true)
    try {
      await fn()
      push(label, detail)
      getChaosState().then(setState).catch(() => {})
    } catch (err) {
      push(`${label} failed`, err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const push = (title: string, detail?: string) =>
    onLocalEvent({ id: `${Date.now()}-${title}`, timestamp: Date.now(), kind: 'chaos', title, detail })

  const activeRules = state?.peers.reduce((s, p) => s + (p.rules?.length ?? 0), 0) ?? 0
  const canAct = !busy && !!cluster

  return (
    <div className="flex flex-col h-full">
      {/* Heal strip */}
      <div className="flex-none flex items-center justify-between px-4 py-2.5 bg-slate-50 border-b border-slate-200">
        <span className="text-xs text-slate-500">
          {activeRules > 0
            ? <span className="font-semibold text-red-600">{activeRules} active rule{activeRules !== 1 ? 's' : ''}</span>
            : <span className="text-green-600 font-medium">No chaos active</span>}
        </span>
        <button
          disabled={!canAct}
          onClick={() => act('Heal all', 'all rules cleared', healAll)}
          className="text-xs px-3 py-1.5 rounded-md bg-green-600 text-white font-semibold hover:bg-green-700 disabled:opacity-40 disabled:cursor-not-allowed transition"
        >
          Heal all
        </button>
      </div>

      {/* Mode tabs */}
      <div className="flex-none flex border-b border-slate-200 bg-slate-50">
        {(['isolate', 'partition', 'delay', 'loss'] as Tab[]).map((t) => (
          <button
            key={t}
            onClick={() => setTab(t)}
            className={`flex-1 py-2 text-[11px] font-semibold tracking-wide uppercase transition-colors
              ${tab === t ? 'text-slate-900 border-b-2 border-slate-800 bg-white' : 'text-slate-400 hover:text-slate-700'}`}
          >
            {t}
          </button>
        ))}
      </div>

      {/* Controls */}
      <div className="flex-1 overflow-y-auto px-4 py-4 space-y-3">
        {tab === 'isolate' && <>
          <Select label="Target node" nodes={nodes} value={target} onChange={setTarget} />
          <Hint>Blackholes <b>{target}</b>: drops all traffic in both directions. Majority elects a new leader within 2–3 election timeouts.</Hint>
          <RunBtn disabled={!canAct || !target} busy={busy} onClick={() =>
            act(`Isolate ${target}`, 'blackholed', () => isolateNode(target))}>
            Isolate {target || '—'}
          </RunBtn>
        </>}

        {tab === 'partition' && <>
          <Select label="Node A" nodes={nodes} value={target} onChange={setTarget} />
          <Select label="Node B" nodes={nodes.filter((n) => n !== target)} value={partner} onChange={setPartner} />
          <Hint>Drops traffic between <b>{target}</b> and <b>{partner}</b> only — other links stay healthy. Great for split-brain experiments.</Hint>
          <RunBtn disabled={!canAct || !target || !partner || target === partner} busy={busy} onClick={() =>
            act(`Partition ${target} ↔ ${partner}`, 'link severed', () => partition(target, partner))}>
            Partition {target || '—'} ↔ {partner || '—'}
          </RunBtn>
        </>}

        {tab === 'delay' && <>
          <Select label="Target node" nodes={nodes} value={target} onChange={setTarget} />
          <NumberField label="Delay (ms)" value={delayMs} onChange={setDelayMs} min={1} max={5000} />
          <NumberField label="Jitter (ms)" value={jitterMs} onChange={setJitterMs} min={0} max={2000} />
          <Hint>Adds <b>{delayMs}ms ± {jitterMs}ms</b> latency to all messages to/from <b>{target}</b>. If delay &gt; 150ms expect repeated elections.</Hint>
          <RunBtn disabled={!canAct || !target || delayMs <= 0} busy={busy} onClick={() =>
            act(`Delay ${target}`, `${delayMs}ms ± ${jitterMs}ms`, () => injectDelay(target, delayMs, jitterMs))}>
            Inject {delayMs}ms delay
          </RunBtn>
        </>}

        {tab === 'loss' && <>
          <Select label="Target node" nodes={nodes} value={target} onChange={setTarget} />
          <div>
            <label className="block text-[10px] uppercase tracking-widest text-slate-500 font-semibold mb-1">
              Loss rate — {(lossRate * 100).toFixed(0)}%
            </label>
            <input type="range" min={0.05} max={0.95} step={0.05} value={lossRate}
              onChange={(e) => setLossRate(parseFloat(e.target.value))}
              className="w-full accent-red-500" />
            <div className="flex justify-between text-[10px] text-slate-400 mt-0.5">
              <span>5%</span><span>95%</span>
            </div>
          </div>
          <Hint>Randomly drops <b>{(lossRate * 100).toFixed(0)}%</b> of packets on <b>{target}</b>. Some AppendEntries succeed, others fail — commits advance but slowly.</Hint>
          <RunBtn disabled={!canAct || !target} busy={busy} onClick={() =>
            act(`${(lossRate * 100).toFixed(0)}% loss on ${target}`, 'flaky link', () => injectLoss(target, lossRate))}>
            Inject {(lossRate * 100).toFixed(0)}% loss
          </RunBtn>
        </>}
      </div>

      {/* Active rules */}
      {state && activeRules > 0 && (
        <div className="flex-none border-t border-slate-200 px-4 py-3 bg-red-50">
          <div className="text-[10px] uppercase tracking-widest text-red-400 font-semibold mb-1.5">Active chaos</div>
          <div className="space-y-1">
            {state.peers.map((p) => p.rules?.length > 0 && (
              <div key={p.node} className="flex items-start gap-2 text-[11px] font-mono">
                <span className="text-slate-500 w-14 flex-none">{p.node}</span>
                <span className="text-red-700">{p.rules.map(ruleLabel).join(' · ')}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  )
}

function ruleLabel(r: { action: string; to_node?: string; params?: Record<string, unknown> }): string {
  const scope = r.to_node ? `→${r.to_node}` : '∗'
  if (r.action === 'drop') return `${scope} drop`
  if (r.action === 'delay') return `${scope} +${r.params?.delay_ms ?? '?'}ms`
  if (r.action === 'loss') return `${scope} ${((Number(r.params?.loss_rate ?? 0)) * 100).toFixed(0)}% loss`
  return `${scope} ${r.action}`
}

function Select({ label, nodes, value, onChange }: { label: string; nodes: string[]; value: string; onChange: (v: string) => void }) {
  return (
    <label className="block">
      <span className="text-[10px] uppercase tracking-widest text-slate-500 font-semibold">{label}</span>
      <select value={value} onChange={(e) => onChange(e.target.value)}
        className="mt-1 w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono text-slate-900 bg-white focus:outline-none focus:border-slate-500 focus:ring-1 focus:ring-slate-500">
        {nodes.map((id) => <option key={id} value={id}>{id}</option>)}
      </select>
    </label>
  )
}

function NumberField({ label, value, onChange, min, max }: { label: string; value: number; onChange: (v: number) => void; min?: number; max?: number }) {
  return (
    <label className="block">
      <span className="text-[10px] uppercase tracking-widest text-slate-500 font-semibold">{label}</span>
      <input type="number" value={value} min={min} max={max}
        onChange={(e) => onChange(parseInt(e.target.value, 10) || 0)}
        className="mt-1 w-full border border-slate-300 rounded-lg px-3 py-2 text-sm font-mono text-slate-900 bg-white focus:outline-none focus:border-slate-500" />
    </label>
  )
}

function Hint({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-xs text-slate-500 bg-slate-50 border border-slate-200 rounded-lg px-3 py-2 leading-relaxed">
      {children}
    </div>
  )
}

function RunBtn({ onClick, disabled, busy, children }: { onClick: () => void; disabled?: boolean; busy?: boolean; children: React.ReactNode }) {
  return (
    <button disabled={disabled || busy} onClick={onClick}
      className="w-full py-2.5 rounded-lg bg-red-600 hover:bg-red-700 text-white text-sm font-semibold transition shadow-sm disabled:opacity-40 disabled:cursor-not-allowed">
      {busy ? <span className="flex items-center justify-center gap-2"><span className="w-3.5 h-3.5 rounded-full border-2 border-white border-t-transparent spin" />…</span> : children}
    </button>
  )
}
