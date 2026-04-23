import { useState } from 'react'
import type { ClusterStatus, LocalEvent } from '../types'
import ScenarioPanel from './ScenarioPanel'
import ChaosPanel from './ChaosPanel'
import KVPanel from './KVPanel'
import SettingsPanel from './SettingsPanel'

type Tab = 'scenarios' | 'chaos' | 'kv' | 'settings'

const tabs: { id: Tab; label: string }[] = [
  { id: 'scenarios', label: 'Scenarios' },
  { id: 'chaos',     label: 'Chaos'     },
  { id: 'kv',        label: 'KV ops'    },
  { id: 'settings',  label: 'Settings'  },
]

interface Props {
  cluster: ClusterStatus | null
  onLocalEvent: (e: LocalEvent) => void
}

export default function ControlColumn({ cluster, onLocalEvent }: Props) {
  const [tab, setTab] = useState<Tab>('scenarios')
  const [healDelay, setHealDelay] = useState(4000)

  const showExplainer = tab !== 'settings'

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

      {/* Content area — split between panel and explainer (except Settings) */}
      {showExplainer ? (
        <div className="flex-1 min-h-0 flex flex-col overflow-hidden">
          {/* Panel — top portion */}
          <div className="flex-[5] min-h-0 overflow-hidden">
            {tab === 'scenarios' && <ScenarioPanel cluster={cluster} healDelay={healDelay} onLocalEvent={onLocalEvent} />}
            {tab === 'chaos'     && <ChaosPanel    cluster={cluster} onLocalEvent={onLocalEvent} />}
            {tab === 'kv'        && <KVPanel leaderId={cluster?.leader_id ?? ''} onLocalEvent={onLocalEvent} />}
          </div>

          {/* Explainer — bottom portion */}
          <div className="flex-[3] min-h-0 border-t border-slate-200 overflow-y-auto bg-slate-50">
            <TabExplainer tab={tab} />
          </div>
        </div>
      ) : (
        <div className="flex-1 min-h-0 overflow-hidden">
          <SettingsPanel
            healDelay={healDelay}
            onHealDelayChange={setHealDelay}
            onLocalEvent={onLocalEvent}
          />
        </div>
      )}
    </div>
  )
}

// ── Contextual explainer ──────────────────────────────────────

function TabExplainer({ tab }: { tab: Tab }) {
  switch (tab) {
    case 'scenarios': return <ScenariosInfo />
    case 'chaos':     return <ChaosInfo />
    case 'kv':        return <KVInfo />
    default:          return null
  }
}

function InfoSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="px-4 pt-3 pb-1">
      <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-2">{title}</div>
      {children}
    </div>
  )
}

function Concept({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-2.5">
      <span className="text-[11px] font-bold text-slate-700">{label} — </span>
      <span className="text-[11px] text-slate-500 leading-relaxed">{children}</span>
    </div>
  )
}

function ScenariosInfo() {
  return (
    <div className="pb-3">
      <InfoSection title="What each scenario does">
        <div className="space-y-2">
          {[
            { name: 'Happy path',         color: 'bg-green-500',  desc: 'Writes 5 keys back-to-back. Watch the replication log fill up — blue cells go green as each entry commits across all nodes.' },
            { name: 'Leader crash',        color: 'bg-red-500',    desc: 'Blackholes the current leader. Followers time out, run pre-vote, elect a replacement. The old leader rejoins as a follower after the heal.' },
            { name: 'Minority partition',  color: 'bg-blue-500',   desc: 'Isolates one follower. The majority keeps committing writes. The isolated node cannot win an election because pre-vote blocks it — no split-brain.' },
            { name: 'Packet loss',         color: 'bg-amber-500',  desc: 'Drops 40% of messages to a follower. AppendEntries retries keep commits advancing, just more slowly. No election fires — resilience without drama.' },
            { name: 'Slow follower',       color: 'bg-purple-500', desc: 'Adds 300ms latency to one follower. The leader uses the fast follower for quorum and commits immediately. The slow one catches up eventually.' },
          ].map(({ name, color, desc }) => (
            <div key={name} className="flex gap-2.5">
              <span className={`mt-1 flex-none w-2 h-2 rounded-full ${color}`} />
              <div>
                <div className="text-[11px] font-bold text-slate-700">{name}</div>
                <div className="text-[11px] text-slate-500 leading-relaxed">{desc}</div>
              </div>
            </div>
          ))}
        </div>
      </InfoSection>
    </div>
  )
}

function ChaosInfo() {
  return (
    <div className="pb-3">
      <InfoSection title="What each chaos mode does">
        <div className="space-y-2.5">
          {[
            { tab: 'Isolate',   color: 'text-red-600',    desc: 'Drops all Raft traffic to and from the target. It loses quorum immediately. Watch the topology turn red and the event feed fire a leader_change within a few election timeouts.' },
            { tab: 'Partition', color: 'text-red-600',    desc: 'Severs only the link between two specific nodes, leaving all other links healthy. Use this to explore split-brain edge cases where neither side has majority.' },
            { tab: 'Delay',     color: 'text-purple-600', desc: 'Adds latency ± jitter to a node\'s Raft messages. If delay exceeds the election timeout, the node looks dead to its peers. Watch term numbers climb as elections fire.' },
            { tab: 'Loss',      color: 'text-amber-600',  desc: 'Drops a random fraction of packets. Commits still happen — AppendEntries retries fill the gaps — but the replication log advances in bursts rather than smoothly.' },
          ].map(({ tab, color, desc }) => (
            <div key={tab} className="flex gap-2.5">
              <span className={`flex-none text-[11px] font-bold w-16 pt-px ${color}`}>{tab}</span>
              <span className="text-[11px] text-slate-500 leading-relaxed">{desc}</span>
            </div>
          ))}
        </div>
      </InfoSection>
      <InfoSection title="What to look for after injecting chaos">
        <div className="space-y-1.5">
          {[
            ['Topology',        'Affected nodes turn red; partitioned links show a dashed red line with ✕'],
            ['Event feed',      'leader_change fires when quorum is lost and a new election succeeds'],
            ['Replication log', 'Isolated node\'s column freezes; it catches up the moment you heal'],
            ['Health pill',     'Flips to PARTITIONED or DEGRADED depending on active rules'],
          ].map(([k, v]) => (
            <div key={k} className="flex gap-2 text-[11px]">
              <span className="flex-none font-semibold text-slate-600 w-24">{k}</span>
              <span className="text-slate-500">{v}</span>
            </div>
          ))}
        </div>
      </InfoSection>
    </div>
  )
}

function KVInfo() {
  return (
    <div className="pb-3">
      <InfoSection title="What these operations do">
        <div className="space-y-2.5 mb-1">
          {[
            { op: 'PUT', color: 'bg-green-600', desc: 'Proposes a write to the leader. The key-value pair travels through the Raft log: leader appends → followers replicate → majority ack → committed → applied to the state machine. Only then does PUT return 200.' },
            { op: 'GET', color: 'bg-blue-600',  desc: 'Reads from the leader\'s in-memory state machine. Because reads are leader-only, the result always reflects the latest committed write — fully linearizable, never stale.' },
            { op: 'DEL', color: 'bg-red-600',   desc: 'Same path as PUT but proposes a delete command. The entry commits through the log and the key is removed from the state machine on every node simultaneously.' },
          ].map(({ op, color, desc }) => (
            <div key={op} className="flex gap-2.5">
              <span className={`flex-none text-[10px] font-bold text-white px-1.5 py-0.5 rounded h-fit ${color}`}>{op}</span>
              <span className="text-[11px] text-slate-500 leading-relaxed">{desc}</span>
            </div>
          ))}
        </div>
      </InfoSection>
      <InfoSection title="What happens if you're not on the leader">
        <p className="text-[11px] text-slate-500 leading-relaxed">
          Any node can receive the request. If it isn't the leader it transparently proxies
          the call to whoever is — you'll see <span className="font-mono text-slate-700">Routed to leader: nodeX</span> at
          the top. Try injecting chaos first, then writing keys: the proxy automatically
          follows the new leader after an election.
        </p>
      </InfoSection>
    </div>
  )
}
