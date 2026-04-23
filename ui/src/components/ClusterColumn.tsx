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
    const ic = setInterval(loadChaos, 1500)
    const il = setInterval(loadLog, 1000)
    return () => { clearInterval(ic); clearInterval(il) }
  }, [])

  const nodes = cluster?.nodes ?? []
  const leader = cluster?.leader_id ?? ''
  const term = cluster?.term ?? 0
  const reachable = nodes.filter((n) => n.reachable).length
  const health = !cluster ? 'unknown'
    : reachable === nodes.length && !!leader ? 'healthy'
    : reachable > nodes.length / 2 ? 'degraded'
    : 'critical'

  const healthColor = health === 'healthy' ? 'bg-green-100 text-green-700 border-green-300'
    : health === 'degraded' ? 'bg-amber-100 text-amber-700 border-amber-300'
    : 'bg-red-100 text-red-700 border-red-300'

  return (
    <div className="flex-1 flex flex-col overflow-y-auto bg-slate-50">
      {/* Stats strip */}
      <div className="flex-none flex items-center gap-3 px-4 py-2.5 bg-white border-b border-slate-200 flex-wrap">
        <Pill label="health" value={health.toUpperCase()} className={healthColor} />
        <Pill label="leader" value={leader || '—'} className="bg-green-50 text-green-800 border-green-200" />
        <Pill label="term" value={String(term)} className="bg-slate-100 text-slate-700 border-slate-200" />
        <Pill label="nodes" value={`${reachable}/${nodes.length} up`} className="bg-slate-100 text-slate-700 border-slate-200" />
      </div>

      {/* Topology */}
      <div className="flex-none p-4">
        <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-2">Topology</div>
        <Topology nodes={nodes} leaderId={leader} chaos={chaos} />
      </div>

      {/* Node cards */}
      <div className="flex-none px-4 pb-4">
        <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-2">Nodes</div>
        <div className="grid grid-cols-3 gap-2">
          {nodes.map((n) => <NodeCard key={n.id} node={n} isLeader={n.id === leader} chaos={chaos} />)}
        </div>
      </div>

      {/* Replication log */}
      <div className="flex-1 px-4 pb-4 min-h-0">
        <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-2">
          Replication log
        </div>
        <LogTable log={log} />
      </div>
    </div>
  )
}

function Pill({ label, value, className }: { label: string; value: string; className: string }) {
  return (
    <div className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full border text-xs font-medium ${className}`}>
      <span className="opacity-60">{label}</span>
      <span className="font-semibold">{value}</span>
    </div>
  )
}

// ── Topology SVG ─────────────────────────────────────────────

interface EdgeState { drop: boolean; delayMs?: number; lossRate?: number }

function buildEdgeMap(chaos: ChaosState | null, nodeIds: string[]): Map<string, EdgeState> {
  const edges = new Map<string, EdgeState>()
  if (!chaos) return edges
  const ensure = (k: string): EdgeState => {
    if (!edges.has(k)) edges.set(k, { drop: false })
    return edges.get(k)!
  }
  for (const peer of chaos.peers) {
    for (const rule of peer.rules ?? []) {
      const targets = rule.to_node ? [rule.to_node] : nodeIds.filter((n) => n !== peer.node)
      for (const to of targets) {
        const e = ensure(`${peer.node}->${to}`)
        if (rule.action === 'drop') e.drop = true
        if (rule.action === 'delay') e.delayMs = Math.max(e.delayMs ?? 0, Number(rule.params?.delay_ms ?? 0))
        if (rule.action === 'loss') e.lossRate = Math.max(e.lossRate ?? 0, Number(rule.params?.loss_rate ?? 0))
      }
    }
  }
  return edges
}

function Topology({ nodes, leaderId, chaos }: { nodes: NodeStatus[]; leaderId: string; chaos: ChaosState | null }) {
  const W = 480, H = 200
  const cx = W / 2, cy = H / 2
  const r = Math.min(W, H) * 0.33

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
    <div className="rounded-xl border border-slate-200 bg-white overflow-hidden shadow-sm">
      <svg viewBox={`0 0 ${W} ${H}`} className="w-full h-[200px]">
        <defs>
          <filter id="glow-light">
            <feGaussianBlur stdDeviation="5" result="blur" />
            <feComposite in="SourceGraphic" in2="blur" operator="over" />
          </filter>
        </defs>

        {pairs.map(({ from, to }) => {
          const a = posById.get(from)!
          const b = posById.get(to)!
          if (!a || !b) return null
          const ab = edgeMap.get(`${from}->${to}`)
          const ba = edgeMap.get(`${to}->${from}`)
          const dropped = !!ab?.drop || !!ba?.drop
          const delayMs = Math.max(ab?.delayMs ?? 0, ba?.delayMs ?? 0)
          const lossRate = Math.max(ab?.lossRate ?? 0, ba?.lossRate ?? 0)
          const aR = nodes.find((n) => n.id === from)?.reachable
          const bR = nodes.find((n) => n.id === to)?.reachable
          const midX = (a.x + b.x) / 2, midY = (a.y + b.y) / 2

          let stroke = '#e2e8f0', sw = 1.5, animate = false, label: string | null = null
          const isLeaderEdge = from === leaderId || to === leaderId

          if (!dropped && delayMs === 0 && lossRate === 0 && aR && bR) {
            stroke = isLeaderEdge ? '#16a34a' : '#94a3b8'
            sw = isLeaderEdge ? 2 : 1.5
            animate = isLeaderEdge
          }
          if (delayMs > 0) { stroke = '#9333ea'; sw = 2; animate = true; label = `${delayMs}ms` }
          if (lossRate > 0) { stroke = '#d97706'; sw = 2; animate = true; label = `${Math.round(lossRate * 100)}%` }
          if (dropped) { stroke = '#dc2626'; sw = 2; animate = false }

          return (
            <g key={`${from}-${to}`}>
              <line x1={a.x} y1={a.y} x2={b.x} y2={b.y}
                stroke={stroke} strokeWidth={sw}
                strokeDasharray={dropped ? '5 5' : undefined}
                className={animate ? 'edge-animate' : ''}
              />
              {dropped && (
                <g transform={`translate(${midX},${midY})`}>
                  <circle r="10" fill="white" stroke="#dc2626" strokeWidth="1.5" />
                  <path d="M-4-4L4 4M-4 4L4-4" stroke="#dc2626" strokeWidth="1.8" strokeLinecap="round" />
                </g>
              )}
              {label && !dropped && (
                <g transform={`translate(${midX},${midY - 8})`}>
                  <rect x="-22" y="-8" width="44" height="16" rx="8" fill="white" stroke={stroke} strokeWidth="1" />
                  <text textAnchor="middle" dy="3.5" fontSize="9" fill={stroke} fontFamily="monospace">{label}</text>
                </g>
              )}
            </g>
          )
        })}

        {positions.map((p) => {
          const color = nodeColor(p.node)
          const isLeader = p.node.id === leaderId
          return (
            <g key={p.node.id} transform={`translate(${p.x},${p.y})`}>
              {isLeader && <circle r="26" fill={color} opacity="0.15" />}
              <circle r="18" fill={color} />
              <circle r="18" fill="white" opacity="0.25" />
              {/* State letter */}
              <text textAnchor="middle" dy="4" fontSize="11" fill="white" fontWeight="700" fontFamily="monospace">
                {p.node.reachable ? p.node.state[0] : '✕'}
              </text>
              <text textAnchor="middle" dy="-24" fontSize="10" fill="#374151" fontFamily="monospace">
                {p.node.id}
              </text>
              <text textAnchor="middle" dy="32" fontSize="9" fill={color} fontFamily="monospace">
                {p.node.reachable ? p.node.state.toLowerCase() : 'down'}
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
  const partitioned = chaos?.peers.some(
    (p) => p.node === node.id && p.rules?.some((r) => r.action === 'drop')
  ) ?? false

  const effective = node.reachable ? node.state : 'Down'
  const { border, bg, text, dot } = stateTheme(effective, partitioned)

  return (
    <div className={`rounded-lg border ${border} ${bg} p-2.5`}>
      <div className="flex items-center justify-between mb-1.5">
        <span className="font-mono text-xs font-semibold text-slate-800">{node.id}</span>
        <span className={`w-2 h-2 rounded-full ${dot} ${isLeader ? 'leader-pulse' : ''}`} />
      </div>
      <div className={`text-[10px] font-bold uppercase tracking-wider ${text} mb-1.5`}>
        {partitioned ? 'partitioned' : effective}
      </div>
      <div className="space-y-0.5 font-mono text-[10px]">
        <div className="flex justify-between text-slate-500">
          <span>term</span><span className="text-slate-700">{node.term}</span>
        </div>
        <div className="flex justify-between text-slate-500">
          <span>commit</span><span className="text-slate-700">{node.commit_index}</span>
        </div>
      </div>
    </div>
  )
}

function stateTheme(state: string, partitioned: boolean) {
  if (partitioned) return {
    border: 'border-red-300', bg: 'bg-red-50', text: 'text-red-600', dot: 'bg-red-500',
  }
  switch (state) {
    case 'Leader':    return { border: 'border-green-300', bg: 'bg-green-50', text: 'text-green-700', dot: 'bg-green-500' }
    case 'Follower':  return { border: 'border-blue-200',  bg: 'bg-blue-50',  text: 'text-blue-600',  dot: 'bg-blue-500' }
    case 'Candidate': return { border: 'border-amber-300', bg: 'bg-amber-50', text: 'text-amber-700', dot: 'bg-amber-500' }
    default:          return { border: 'border-red-200',   bg: 'bg-red-50',   text: 'text-red-600',   dot: 'bg-red-400' }
  }
}

function nodeColor(n: NodeStatus): string {
  if (!n.reachable) return '#ef4444'
  switch (n.state) {
    case 'Leader':    return '#16a34a'
    case 'Follower':  return '#2563eb'
    case 'Candidate': return '#d97706'
    default:          return '#94a3b8'
  }
}

// ── Log table ─────────────────────────────────────────────────

function LogTable({ log }: { log: ClusterLogView | null }) {
  if (!log) return <div className="text-xs text-slate-400 py-4 text-center">Loading log…</div>

  const indexSet = new Set<number>()
  for (const n of log.nodes) for (const e of n.entries ?? []) indexSet.add(e.index)
  const allIndexes = Array.from(indexSet).sort((a, b) => b - a)

  if (allIndexes.length === 0)
    return <div className="text-xs text-slate-400 py-4 text-center italic">No entries yet — submit a PUT.</div>

  const nodeMaps = log.nodes.map((n) => {
    const m = new Map(n.entries?.map((e) => [e.index, e]))
    return { node: n, map: m }
  })

  const maxCommit = Math.max(0, ...log.nodes.map((n) => n.commit_index))
  const maxLast   = Math.max(0, ...log.nodes.map((n) => n.last_index))

  return (
    <div className="rounded-lg border border-slate-200 bg-white shadow-sm overflow-hidden">
      {/* progress bar */}
      <div className="h-1 bg-slate-100">
        <div
          className="h-full bg-green-500 transition-all"
          style={{ width: `${maxLast ? (maxCommit / maxLast) * 100 : 0}%` }}
        />
      </div>
      <div className="overflow-x-auto">
        <table className="w-full text-[10px] font-mono">
          <thead>
            <tr className="border-b border-slate-100">
              <th className="text-left px-2 py-1.5 text-slate-400 font-semibold">idx</th>
              {nodeMaps.map(({ node }) => (
                <th key={node.node} className="text-left px-2 py-1.5 text-slate-400 font-semibold">{node.node}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {allIndexes.map((idx) => (
              <tr key={idx} className="border-t border-slate-50">
                <td className="px-2 py-1 text-slate-400">{idx}</td>
                {nodeMaps.map(({ node, map }) => {
                  const e = map.get(idx)
                  if (!e) return <td key={node.node} className="px-2 py-1 text-slate-300">—</td>
                  const cls = e.committed
                    ? 'bg-green-100 text-green-700 border border-green-200'
                    : 'bg-blue-100 text-blue-700 border border-blue-200'
                  return (
                    <td key={node.node} className="px-2 py-0.5">
                      <span className={`inline-block px-1.5 py-0.5 rounded text-[10px] ${cls}`} title={e.data}>
                        t{e.term} {(e.data ?? '').slice(0, 12)}
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
