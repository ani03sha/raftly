import type { ClusterEvent, LocalEvent } from '../types'

interface Props {
  events: ClusterEvent[]
  localEvents: LocalEvent[]
}

type Unified =
  | { kind: 'cluster'; ts: number; event: ClusterEvent }
  | { kind: 'local';   ts: number; event: LocalEvent   }

const clusterStyle: Record<string, { dot: string; label: string; text: string; bg: string; border: string }> = {
  leader_change: { dot: 'bg-green-500',  label: 'Leader elected',  text: 'text-green-700',  bg: 'bg-green-50',  border: 'border-green-200' },
  term_change:   { dot: 'bg-amber-500',  label: 'Term advanced',   text: 'text-amber-700',  bg: 'bg-amber-50',  border: 'border-amber-200' },
  node_down:     { dot: 'bg-red-500',    label: 'Node down',       text: 'text-red-700',    bg: 'bg-red-50',    border: 'border-red-200'   },
  node_up:       { dot: 'bg-blue-500',   label: 'Node recovered',  text: 'text-blue-700',   bg: 'bg-blue-50',   border: 'border-blue-200'  },
}

const localStyle: Record<LocalEvent['kind'], { dot: string; label: string; text: string; bg: string; border: string }> = {
  chaos:    { dot: 'bg-red-400',    label: 'Chaos',    text: 'text-red-700',    bg: 'bg-red-50',    border: 'border-red-200'    },
  kv:       { dot: 'bg-slate-400',  label: 'KV',       text: 'text-slate-700',  bg: 'bg-slate-50',  border: 'border-slate-200'  },
  scenario: { dot: 'bg-purple-400', label: 'Scenario', text: 'text-purple-700', bg: 'bg-purple-50', border: 'border-purple-200' },
}

export default function EventFeed({ events, localEvents }: Props) {
  const unified: Unified[] = [
    ...events.filter((e) => e.type !== 'status').map((e) => ({ kind: 'cluster' as const, ts: e.timestamp, event: e })),
    ...localEvents.map((e) => ({ kind: 'local' as const, ts: e.timestamp, event: e })),
  ].sort((a, b) => b.ts - a.ts)

  return (
    <div className="flex flex-col h-full">
      <div className="flex-none px-4 py-3 border-b border-slate-200 bg-slate-50">
        <div className="text-xs font-semibold text-slate-700">Event timeline</div>
        <div className="text-[10px] text-slate-400 mt-0.5">{unified.length} events</div>
      </div>

      <div className="flex-1 overflow-y-auto px-3 py-3 space-y-1.5">
        {unified.length === 0 && (
          <div className="text-xs text-slate-400 text-center py-8 italic">
            No events yet. Run a scenario or inject chaos.
          </div>
        )}
        {unified.map((u, i) => {
          if (u.kind === 'cluster') {
            const s = clusterStyle[u.event.type]
            if (!s) return null
            const data = u.event.data as Record<string, unknown> | undefined
            return (
              <div key={`c-${i}-${u.ts}`} className={`rounded-lg border ${s.border} ${s.bg} px-2.5 py-2 slide-in`}>
                <div className="flex items-center gap-1.5 mb-0.5">
                  <span className={`w-1.5 h-1.5 rounded-full flex-none ${s.dot}`} />
                  <span className={`text-[10px] font-bold uppercase tracking-wider ${s.text}`}>{s.label}</span>
                  <span className="ml-auto text-[10px] text-slate-400">{new Date(u.ts).toLocaleTimeString()}</span>
                </div>
                <div className="text-xs text-slate-600 font-mono pl-3">
                  {formatCluster(u.event, data)}
                </div>
              </div>
            )
          } else {
            const s = localStyle[u.event.kind]
            return (
              <div key={u.event.id} className={`rounded-lg border ${s.border} ${s.bg} px-2.5 py-2 slide-in`}>
                <div className="flex items-center gap-1.5 mb-0.5">
                  <span className={`w-1.5 h-1.5 rounded-full flex-none ${s.dot}`} />
                  <span className={`text-[10px] font-bold uppercase tracking-wider ${s.text}`}>{s.label}</span>
                  <span className="ml-auto text-[10px] text-slate-400">{new Date(u.ts).toLocaleTimeString()}</span>
                </div>
                <div className="text-xs text-slate-700 pl-3 font-medium">{u.event.title}</div>
                {u.event.detail && <div className="text-[11px] text-slate-500 pl-3">{u.event.detail}</div>}
              </div>
            )
          }
        })}
      </div>
    </div>
  )
}

function formatCluster(e: ClusterEvent, data: Record<string, unknown> | undefined): string {
  if (!data) return e.node_id ?? ''
  switch (e.type) {
    case 'leader_change': return `${data.from || '—'} → ${data.to}`
    case 'term_change':   return `term ${data.from} → ${data.to}`
    case 'node_down':
    case 'node_up':       return String(data.id ?? e.node_id ?? '')
    default:              return JSON.stringify(data)
  }
}
