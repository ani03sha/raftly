import { useState } from 'react'
import type { ClusterStatus, LocalEvent } from '../types'
import { healAll, injectDelay, injectLoss, isolateNode, putKey } from '../api'

interface Props {
  cluster: ClusterStatus | null
  onLocalEvent: (e: LocalEvent) => void
}

interface Scenario {
  id: string
  title: string
  summary: string
  steps: string[]
  accent: 'green' | 'red' | 'blue' | 'amber' | 'purple'
  run: (ctx: RunContext) => Promise<void>
}

interface RunContext {
  cluster: ClusterStatus
  log: (title: string, detail?: string) => void
  sleep: (ms: number) => Promise<void>
}

const scenarios: Scenario[] = [
  {
    id: 'happy-path',
    title: 'Happy path',
    summary: 'Write 5 keys and watch them replicate and commit on every node.',
    accent: 'green',
    steps: [
      'PUT 5 keys via the current leader',
      'Followers receive AppendEntries',
      'All nodes advance commit index',
    ],
    async run({ cluster, log, sleep }) {
      log('Starting happy-path', `leader is ${cluster.leader_id}`)
      for (let i = 1; i <= 5; i++) {
        await putKey(`demo-${i}`, `value-${i}`)
        log(`PUT demo-${i}`, `= value-${i}`)
        await sleep(250)
      }
    },
  },
  {
    id: 'leader-crash',
    title: 'Leader crash',
    summary: 'Isolate the leader. The remaining majority elects a new one within a few election timeouts.',
    accent: 'red',
    steps: [
      'Blackhole all traffic to/from the leader',
      'Followers stop receiving heartbeats',
      'Pre-vote + election produces new leader',
      'Heal after 4s — old leader rejoins as follower',
    ],
    async run({ cluster, log, sleep }) {
      const l = cluster.leader_id
      if (!l) { log('No leader found'); return }
      log(`Isolating leader ${l}`)
      await isolateNode(l)
      await sleep(4000)
      log('Healing', 'old leader rejoins as follower')
      await healAll()
    },
  },
  {
    id: 'minority-partition',
    title: 'Minority partition',
    summary: "Isolate one follower. The majority keeps committing. Raft never allows two leaders.",
    accent: 'blue',
    steps: [
      'Isolate a follower from both peers',
      'Isolated node cannot win election (pre-vote blocked)',
      'Majority keeps committing writes',
      'Heal after 5s — node catches up via log replication',
    ],
    async run({ cluster, log, sleep }) {
      const f = cluster.nodes.find((n) => n.id !== cluster.leader_id && n.reachable)?.id
      if (!f) { log('No follower available'); return }
      log(`Isolating follower ${f}`)
      await isolateNode(f)
      await sleep(1200)
      for (let i = 1; i <= 3; i++) {
        await putKey(`majority-${i}`, `offline`)
        log(`PUT majority-${i}`, 'majority commits')
        await sleep(400)
      }
      await sleep(2000)
      log('Healing', `${f} catches up via AppendEntries`)
      await healAll()
    },
  },
  {
    id: 'packet-loss',
    title: 'Packet loss',
    summary: 'Inject 40% loss on a follower. Commits stall and restart as retries succeed.',
    accent: 'amber',
    steps: [
      'Inject 40% packet loss on a follower',
      'AppendEntries succeed or fail at random',
      'Commit index advances slowly via retries',
      'Heal after 6s',
    ],
    async run({ cluster, log, sleep }) {
      const f = cluster.nodes.find((n) => n.id !== cluster.leader_id && n.reachable)?.id
      if (!f) return
      log(`40% loss on ${f}`)
      await injectLoss(f, 0.4)
      for (let i = 1; i <= 4; i++) {
        await putKey(`flaky-${i}`, 'retry')
        log(`PUT flaky-${i}`, 'may be slow')
        await sleep(500)
      }
      await sleep(2000)
      await healAll()
      log('Healed')
    },
  },
  {
    id: 'slow-follower',
    title: 'Slow follower',
    summary: 'Add 300ms latency to one follower. Leader achieves majority quorum from the fast peer.',
    accent: 'purple',
    steps: [
      'Inject 300ms latency on a follower',
      'Leader uses the fast follower for majority',
      'Slow follower lags but eventually catches up',
      'Heal after 5s',
    ],
    async run({ cluster, log, sleep }) {
      const f = cluster.nodes.find((n) => n.id !== cluster.leader_id && n.reachable)?.id
      if (!f) return
      log(`300ms delay on ${f}`)
      await injectDelay(f, 300, 50)
      for (let i = 1; i <= 4; i++) {
        await putKey(`slow-${i}`, 'fast majority')
        log(`PUT slow-${i}`)
        await sleep(400)
      }
      await sleep(1500)
      await healAll()
      log('Healed', `${f} catches up`)
    },
  },
]

const accentTheme: Record<Scenario['accent'], {
  tab: string; tabActive: string; border: string; header: string; badge: string; btn: string; numBg: string
}> = {
  green:  { tab: 'hover:text-green-700',  tabActive: 'text-green-700 border-b-2 border-green-500',  border: 'border-green-200',  header: 'bg-green-50  border-b border-green-200',  badge: 'bg-green-100  text-green-700  border-green-300',  btn: 'bg-green-600  hover:bg-green-700  text-white',  numBg: 'bg-green-100  text-green-700  border-green-300'  },
  red:    { tab: 'hover:text-red-700',    tabActive: 'text-red-700 border-b-2 border-red-500',      border: 'border-red-200',    header: 'bg-red-50    border-b border-red-200',    badge: 'bg-red-100    text-red-700    border-red-300',    btn: 'bg-red-600    hover:bg-red-700    text-white',    numBg: 'bg-red-100    text-red-700    border-red-300'    },
  blue:   { tab: 'hover:text-blue-700',   tabActive: 'text-blue-700 border-b-2 border-blue-500',    border: 'border-blue-200',   header: 'bg-blue-50   border-b border-blue-200',   badge: 'bg-blue-100   text-blue-700   border-blue-300',   btn: 'bg-blue-600   hover:bg-blue-700   text-white',    numBg: 'bg-blue-100   text-blue-700   border-blue-300'   },
  amber:  { tab: 'hover:text-amber-700',  tabActive: 'text-amber-700 border-b-2 border-amber-500',  border: 'border-amber-200',  header: 'bg-amber-50  border-b border-amber-200',  badge: 'bg-amber-100  text-amber-700  border-amber-300',  btn: 'bg-amber-600  hover:bg-amber-700  text-white',   numBg: 'bg-amber-100  text-amber-700  border-amber-300'  },
  purple: { tab: 'hover:text-purple-700', tabActive: 'text-purple-700 border-b-2 border-purple-500',border: 'border-purple-200', header: 'bg-purple-50 border-b border-purple-200', badge: 'bg-purple-100 text-purple-700 border-purple-300', btn: 'bg-purple-600 hover:bg-purple-700 text-white',  numBg: 'bg-purple-100 text-purple-700 border-purple-300' },
}

export default function ScenarioPanel({ cluster, onLocalEvent }: Props) {
  const [activeTab, setActiveTab] = useState(scenarios[0].id)
  const [runningId, setRunningId] = useState<string | null>(null)

  const active = scenarios.find((s) => s.id === activeTab) ?? scenarios[0]
  const theme = accentTheme[active.accent]
  const isRunning = runningId !== null

  const run = async (s: Scenario) => {
    if (!cluster) return
    setRunningId(s.id)
    const push = (title: string, detail?: string) =>
      onLocalEvent({ id: `${Date.now()}-${title}`, timestamp: Date.now(), kind: 'scenario', title: `[${s.title}] ${title}`, detail })
    try {
      await s.run({ cluster, log: push, sleep: (ms) => new Promise((r) => setTimeout(r, ms)) })
    } catch (err) {
      push('error', err instanceof Error ? err.message : String(err))
    } finally {
      setRunningId(null)
    }
  }

  return (
    <div className="flex flex-col h-full">
      {/* Scenario tabs */}
      <div className="flex border-b border-slate-200 bg-slate-50 overflow-x-auto flex-none">
        {scenarios.map((s) => {
          const t = accentTheme[s.accent]
          const active = s.id === activeTab
          return (
            <button
              key={s.id}
              onClick={() => setActiveTab(s.id)}
              className={`flex-none px-3.5 py-2.5 text-[11px] font-semibold whitespace-nowrap transition-colors
                ${active ? t.tabActive : `text-slate-400 ${t.tab}`}
                ${runningId === s.id ? 'opacity-70' : ''}`}
            >
              {s.title}
              {runningId === s.id && <span className="ml-1.5 inline-block w-2 h-2 rounded-full border border-current border-t-transparent spin" />}
            </button>
          )
        })}
      </div>

      {/* Active scenario */}
      <div className="flex-1 overflow-y-auto">
        {/* Summary */}
        <div className={`px-4 py-3 ${theme.header}`}>
          <p className="text-sm text-slate-700 leading-relaxed">{active.summary}</p>
        </div>

        {/* Steps */}
        <div className="px-4 py-3">
          <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-2">What happens</div>
          <ol className="space-y-2">
            {active.steps.map((step, i) => (
              <li key={i} className="flex items-start gap-2.5 text-sm text-slate-600">
                <span className={`flex-none w-5 h-5 rounded-full border ${theme.numBg} text-[10px] font-bold flex items-center justify-center mt-0.5`}>
                  {i + 1}
                </span>
                {step}
              </li>
            ))}
          </ol>
        </div>

        {/* Run button */}
        <div className="px-4 pb-4">
          <button
            disabled={!cluster || isRunning}
            onClick={() => run(active)}
            className={`w-full py-2.5 rounded-lg text-sm font-semibold transition shadow-sm disabled:opacity-40 disabled:cursor-not-allowed ${theme.btn}`}
          >
            {runningId === active.id ? (
              <span className="flex items-center justify-center gap-2">
                <span className="w-3.5 h-3.5 rounded-full border-2 border-white border-t-transparent spin" />
                Running…
              </span>
            ) : (
              'Run scenario'
            )}
          </button>
          {!cluster && <p className="text-xs text-slate-400 mt-1.5 text-center">Waiting for cluster…</p>}
        </div>
      </div>
    </div>
  )
}
