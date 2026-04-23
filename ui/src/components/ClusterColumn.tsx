import { useEffect, useState } from 'react'
import type { ChaosState, ClusterStatus, NodeStatus } from '../types'
import { getChaosState, getClusterLog } from '../api'
import type { ClusterLogView } from '../types'

export default function ClusterColumn({ cluster }: { cluster: ClusterStatus | null }) {
  const [chaos, setChaos] = useState<ChaosState | null>(null)
  const [log, setLog] = useState<ClusterLogView | null>(null)

  useEffect(() => {
    const loadChaos = () => getChaosState().then(setChaos).catch(() => {})
    const loadLog = () => getClusterLog(10).then(setLog).catch(() => {})
    loadChaos(); loadLog()
    const ic = setInterval(loadChaos, 1000)
    const il = setInterval(loadLog, 1000)
    return () => { clearInterval(ic); clearInterval(il) }
  }, [])

  const nodes = cluster?.nodes ?? []
  const leader = cluster?.leader_id ?? ''
  const term = cluster?.term ?? 0
  const reachable = nodes.filter((n) => n.reachable).length
  const anyPartitioned = chaos?.peers.some((p) => p.rules?.some((r) => r.action === 'drop')) ?? false

  const health =
    !cluster         ? 'unknown' :
    anyPartitioned   ? 'partitioned' :
    reachable === nodes.length && !!leader ? 'healthy' :
    reachable > nodes.length / 2           ? 'degraded' :
                                             'critical'

  const healthCls =
    health === 'healthy'     ? 'bg-emerald-50 text-emerald-700 border-emerald-200' :
    health === 'partitioned' ? 'bg-rose-50 text-rose-700 border-rose-200' :
    health === 'degraded'    ? 'bg-amber-50 text-amber-700 border-amber-200' :
    health === 'critical'    ? 'bg-rose-50 text-rose-700 border-rose-200' :
                               'bg-slate-100 text-slate-500 border-slate-200'

  return (
    <div className="flex flex-col h-full">
      {/* Stats strip */}
      <div className="flex-none flex items-center gap-2 px-3 py-2 bg-white border-b border-slate-200 flex-wrap">
        <Pill label="health" value={health.toUpperCase()} className={healthCls} />
        <Pill label="leader" value={leader || '—'} className="bg-emerald-50 text-emerald-800 border-emerald-200" />
        <Pill label="term"   value={String(term)}   className="bg-slate-100 text-slate-600 border-slate-200" />
        <Pill label="nodes"  value={`${reachable}/${nodes.length}`} className="bg-slate-100 text-slate-600 border-slate-200" />
      </div>

      {/* Topology */}
      <div className="flex-none px-3 pt-3 pb-2">
        <SectionLabel>Topology</SectionLabel>
        <Topology nodes={nodes} leaderId={leader} chaos={chaos} />
      </div>

      {/* Node cards */}
      <div className="flex-none px-3 pb-3">
        <SectionLabel>Nodes</SectionLabel>
        <div className="grid grid-cols-3 gap-1.5">
          {nodes.map((n) => (
            <NodeCard key={n.id} node={n} isLeader={n.id === leader} chaos={chaos} />
          ))}
        </div>
      </div>

      {/* Replication log */}
      <div className="flex-1 min-h-0 px-3 pb-3">
        <SectionLabel>Replication log</SectionLabel>
        <LogTable log={log} />
      </div>
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-1.5">
      {children}
    </div>
  )
}

function Pill({ label, value, className }: { label: string; value: string; className: string }) {
  return (
    <div className={`inline-flex items-center gap-1 px-2 py-0.5 rounded-full border text-[11px] font-medium ${className}`}>
      <span className="opacity-60">{label}</span>
      <span className="font-bold">{value}</span>
    </div>
  )
}

// ── Topology SVG ──────────────────────────────────────────────

interface EdgeState { drop: boolean; delayMs?: number; lossRate?: number }

function buildEdgeMap(chaos: ChaosState | null, nodeIds: string[]): Map<string, EdgeState> {
  const m = new Map<string, EdgeState>()
  if (!chaos) return m
  const get = (k: string): EdgeState => { if (!m.has(k)) m.set(k, { drop: false }); return m.get(k)! }
  for (const peer of chaos.peers) {
    for (const rule of peer.rules ?? []) {
      const targets = rule.to_node ? [rule.to_node] : nodeIds.filter((n) => n !== peer.node)
      for (const to of targets) {
        const e = get(`${peer.node}->${to}`)
        if (rule.action === 'drop')  e.drop     = true
        if (rule.action === 'delay') e.delayMs  = Math.max(e.delayMs  ?? 0, Number(rule.params?.delay_ms  ?? 0))
        if (rule.action === 'loss')  e.lossRate = Math.max(e.lossRate ?? 0, Number(rule.params?.loss_rate ?? 0))
      }
    }
  }
  return m
}

// Returns whether a node is partitioned (has any drop rule on its outbound)
function isPartitioned(nodeId: string, chaos: ChaosState | null): boolean {
  return chaos?.peers.some((p) => p.node === nodeId && p.rules?.some((r) => r.action === 'drop')) ?? false
}

function nodeCircleColor(n: NodeStatus, chaos: ChaosState | null): string {
  if (!n.reachable || isPartitioned(n.id, chaos)) return '#e11d48' // rose-600
  switch (n.state) {
    case 'Leader':                         return '#059669' // emerald-600
    case 'Follower':                       return '#0284c7' // sky-600
    case 'Candidate': case 'PreCandidate': return '#d97706' // amber-600
    default:                               return '#e11d48' // rose-600
  }
}

function Topology({ nodes, leaderId, chaos }: { nodes: NodeStatus[]; leaderId: string; chaos: ChaosState | null }) {
  const W = 340, H = 190
  const cx = W / 2, cy = H / 2
  const r = Math.min(W, H) * 0.34

  const positions = nodes.map((node, i) => {
    const angle = (2 * Math.PI * i) / Math.max(nodes.length, 1) - Math.PI / 2
    return { node, x: cx + r * Math.cos(angle), y: cy + r * Math.sin(angle) }
  })
  const posById = new Map(positions.map((p) => [p.node.id, p]))
  const edgeMap = buildEdgeMap(chaos, nodes.map((n) => n.id))

  const pairs: { from: string; to: string }[] = []
  for (let i = 0; i < nodes.length; i++)
    for (let j = i + 1; j < nodes.length; j++)
      pairs.push({ from: nodes[i].id, to: nodes[j].id })

  return (
    <div className="rounded-lg border border-slate-200 bg-white shadow-sm overflow-hidden">
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full h-[190px]">

        {pairs.map(({ from, to }) => {
          const a = posById.get(from)!
          const b = posById.get(to)!
          if (!a || !b) return null
          const ab = edgeMap.get(`${from}->${to}`)
          const ba = edgeMap.get(`${to}->${from}`)
          const dropped  = !!ab?.drop || !!ba?.drop
          const delayMs  = Math.max(ab?.delayMs  ?? 0, ba?.delayMs  ?? 0)
          const lossRate = Math.max(ab?.lossRate ?? 0, ba?.lossRate ?? 0)
          const midX = (a.x + b.x) / 2, midY = (a.y + b.y) / 2
          const isLeaderEdge = from === leaderId || to === leaderId
          const bothUp = a.node.reachable && b.node.reachable

          let stroke = '#cbd5e1', sw = 1.5, animate = false, label: string | null = null

          if (!dropped && delayMs === 0 && lossRate === 0 && bothUp) {
            stroke = isLeaderEdge ? '#059669' : '#7dd3fc'
            sw = isLeaderEdge ? 2.5 : 1.5
            animate = isLeaderEdge
          }
          if (delayMs  > 0) { stroke = '#9333ea'; sw = 2; animate = true;  label = `${delayMs}ms` }
          if (lossRate > 0) { stroke = '#d97706'; sw = 2; animate = true;  label = `${Math.round(lossRate * 100)}%` }
          if (dropped)      { stroke = '#e11d48'; sw = 2; animate = false }

          return (
            <g key={`${from}-${to}`}>
              <line x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                stroke={stroke} strokeWidth={sw}
                strokeDasharray={dropped ? '5 5' : undefined}
                className={animate ? 'edge-animate' : ''}
                opacity={bothUp ? 1 : 0.3}
              />
              {dropped && (
                <g transform={`translate(${midX},${midY})`}>
                  <circle r="9" fill="white" stroke="#e11d48" strokeWidth="1.5" />
                  <path d="M-3.5-3.5L3.5 3.5M-3.5 3.5L3.5-3.5" stroke="#e11d48" strokeWidth="1.8" strokeLinecap="round" />
                </g>
              )}
              {label && !dropped && (
                <g transform={`translate(${midX},${midY - 7})`}>
                  <rect x="-20" y="-7" width="40" height="14" rx="7" fill="white" stroke={stroke} strokeWidth="1" />
                  <text textAnchor="middle" dy="3" fontSize="8" fill={stroke} fontFamily="monospace">{label}</text>
                </g>
              )}
            </g>
          )
        })}

        {positions.map((p) => {
          const color = nodeCircleColor(p.node, chaos)
          const isLeader = p.node.id === leaderId
          const partitioned = isPartitioned(p.node.id, chaos)
          const label = partitioned ? '!' : p.node.reachable ? p.node.state[0] : '✕'

          return (
            <g key={p.node.id} transform={`translate(${p.x},${p.y})`}>
              {/* Outer glow for leader */}
              {isLeader && <circle r="24" fill={color} opacity="0.18" />}
              {/* Main circle — solid color, no white wash */}
              <circle r="17" fill={color} />
              {/* State letter — white text, bold */}
              <text textAnchor="middle" dy="4.5" fontSize="12" fill="white" fontWeight="800"
                fontFamily="system-ui, sans-serif" style={{ userSelect: 'none' }}>
                {label}
              </text>
              {/* Node ID above */}
              <text textAnchor="middle" dy="-23" fontSize="10" fill="#374151" fontWeight="600"
                fontFamily="monospace">
                {p.node.id}
              </text>
              {/* State label below */}
              <text textAnchor="middle" dy="33" fontSize="9" fill={color} fontWeight="600"
                fontFamily="monospace">
                {partitioned ? 'partitioned' : p.node.reachable ? p.node.state.toLowerCase() : 'down'}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}

// ── Node card ─────────────────────────────────────────────────

function NodeCard({ node, isLeader, chaos }: { node: NodeStatus; isLeader: boolean; chaos: ChaosState | null }) {
  const partitioned = isPartitioned(node.id, chaos)
  const effective   = !node.reachable ? 'Down' : partitioned ? 'Partitioned' : node.state

  const theme =
    effective === 'Partitioned' ? { border: 'border-rose-200',    bg: 'bg-rose-50',    text: 'text-rose-600',    dot: 'bg-rose-400'    } :
    effective === 'Down'        ? { border: 'border-rose-200',    bg: 'bg-rose-50',    text: 'text-rose-500',    dot: 'bg-rose-300'    } :
    effective === 'Leader'      ? { border: 'border-emerald-200', bg: 'bg-emerald-50', text: 'text-emerald-700', dot: 'bg-emerald-500' } :
    effective === 'Follower'    ? { border: 'border-sky-200',     bg: 'bg-sky-50',     text: 'text-sky-600',     dot: 'bg-sky-400'     } :
    effective === 'Candidate'   ? { border: 'border-amber-200',   bg: 'bg-amber-50',   text: 'text-amber-700',   dot: 'bg-amber-400'   } :
                                  { border: 'border-slate-200',   bg: 'bg-slate-50',   text: 'text-slate-500',   dot: 'bg-slate-300'   }

  return (
    <div className={`rounded-lg border ${theme.border} ${theme.bg} p-2`}>
      <div className="flex items-center justify-between mb-1">
        <span className="font-mono text-[11px] font-bold text-slate-800">{node.id}</span>
        <span className={`w-2 h-2 rounded-full flex-none ${theme.dot} ${isLeader ? 'leader-pulse' : ''}`} />
      </div>
      <div className={`text-[10px] font-bold uppercase tracking-wider ${theme.text} mb-1.5`}>
        {effective}
      </div>
      <div className="space-y-0.5 font-mono text-[10px] text-slate-500">
        <div className="flex justify-between"><span>term</span><span className="text-slate-700">{node.term}</span></div>
        <div className="flex justify-between"><span>commit</span><span className="text-slate-700">{node.commit_index}</span></div>
      </div>
    </div>
  )
}

// ── Log table ─────────────────────────────────────────────────

function LogTable({ log }: { log: ClusterLogView | null }) {
  if (!log) return <div className="text-[11px] text-slate-400 py-4 text-center">Loading…</div>

  const indexSet = new Set<number>()
  for (const n of log.nodes) for (const e of n.entries ?? []) indexSet.add(e.index)
  const allIndexes = Array.from(indexSet).sort((a, b) => b - a)

  if (allIndexes.length === 0)
    return <div className="text-[11px] text-slate-400 py-4 text-center italic">No entries yet — PUT a key.</div>

  const nodeMaps = log.nodes.map((n) => ({
    node: n,
    map: new Map(n.entries?.map((e) => [e.index, e])),
  }))

  const maxCommit = Math.max(0, ...log.nodes.map((n) => n.commit_index))
  const maxLast   = Math.max(0, ...log.nodes.map((n) => n.last_index))

  return (
    <div className="rounded-lg border border-slate-200 bg-white shadow-sm overflow-hidden">
      <div className="h-1 bg-slate-100">
        <div className="h-full bg-green-500 transition-all"
          style={{ width: `${maxLast ? (maxCommit / maxLast) * 100 : 0}%` }} />
      </div>
      <div className="overflow-auto max-h-36">
        <table className="w-full text-[10px] font-mono">
          <thead className="sticky top-0 bg-white">
            <tr className="border-b border-slate-100">
              <th className="text-left px-2 py-1 text-slate-400 font-semibold">idx</th>
              {nodeMaps.map(({ node }) => (
                <th key={node.node} className="text-left px-2 py-1 text-slate-400 font-semibold">{node.node}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {allIndexes.map((idx) => (
              <tr key={idx} className="border-t border-slate-50 hover:bg-slate-50">
                <td className="px-2 py-0.5 text-slate-400">{idx}</td>
                {nodeMaps.map(({ node, map }) => {
                  const e = map.get(idx)
                  if (!e) return <td key={node.node} className="px-2 py-0.5 text-slate-300">—</td>
                  const cls = e.committed
                    ? 'bg-green-100 text-green-700 border border-green-200'
                    : 'bg-blue-100 text-blue-700 border border-blue-200'
                  return (
                    <td key={node.node} className="px-2 py-0.5">
                      <span className={`inline-block px-1 py-0.5 rounded ${cls}`} title={e.data}>
                        t{e.term} {(e.data ?? '').slice(0, 10)}
                      </span>
                    </td>
                  )
                })}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
