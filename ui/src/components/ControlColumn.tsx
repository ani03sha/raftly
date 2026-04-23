import { useState } from 'react'
import type { ClusterStatus, LocalEvent } from '../types'
import ScenarioPanel from './ScenarioPanel'
import ChaosPanel from './ChaosPanel'
import KVPanel from './KVPanel'

type Tab = 'scenarios' | 'chaos' | 'kv'

const tabs: { id: Tab; label: string }[] = [
  { id: 'scenarios', label: 'Scenarios' },
  { id: 'chaos',     label: 'Chaos'     },
  { id: 'kv',        label: 'KV ops'    },
]

interface Props {
  cluster: ClusterStatus | null
  onLocalEvent: (e: LocalEvent) => void
}

export default function ControlColumn({ cluster, onLocalEvent }: Props) {
  const [tab, setTab] = useState<Tab>('scenarios')

  return (
    <div className="flex flex-col h-full">
      {/* Tab bar */}
      <div className="flex-none flex border-b border-slate-200 bg-white">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setTab(t.id)}
            className={`flex-1 py-3 text-xs font-semibold tracking-wide transition-colors
              ${tab === t.id
                ? 'text-slate-900 border-b-2 border-slate-900 bg-white'
                : 'text-slate-400 hover:text-slate-700'}`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {/* Tab content — scrollable */}
      <div className="flex-1 overflow-y-auto">
        {tab === 'scenarios' && <ScenarioPanel cluster={cluster} onLocalEvent={onLocalEvent} />}
        {tab === 'chaos'     && <ChaosPanel    cluster={cluster} onLocalEvent={onLocalEvent} />}
        {tab === 'kv'        && <KVPanel leaderId={cluster?.leader_id ?? ''} onLocalEvent={onLocalEvent} />}
      </div>
    </div>
  )
}
