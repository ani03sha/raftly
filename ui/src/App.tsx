import { useEffect, useState } from 'react'
import ClusterColumn from './components/ClusterColumn'
import ControlColumn from './components/ControlColumn'
import EventFeed from './components/EventFeed'
import type { ClusterStatus, ClusterEvent, LocalEvent } from './types'
import { getCluster, subscribeEvents } from './api'

export default function App() {
  const [cluster, setCluster] = useState<ClusterStatus | null>(null)
  const [events, setEvents] = useState<ClusterEvent[]>([])
  const [localEvents, setLocalEvents] = useState<LocalEvent[]>([])
  const [connected, setConnected] = useState(false)

  useEffect(() => {
    getCluster().then(setCluster).catch(() => {})
    const unsub = subscribeEvents(
      (e) => {
        if (e.type === 'status') setCluster(e.data as ClusterStatus)
        else setEvents((prev) => [e, ...prev].slice(0, 200))
      },
      () => setConnected(true),
      () => setConnected(false),
    )
    return unsub
  }, [])

  const pushLocal = (e: LocalEvent) =>
    setLocalEvents((prev) => [e, ...prev].slice(0, 200))

  return (
    <div className="flex flex-col h-screen overflow-hidden bg-slate-100">
      {/* ── Header ── */}
      <header className="flex-none h-[52px] bg-white border-b border-slate-200 flex items-center justify-between px-5 shadow-sm z-10">
        <div className="flex items-center gap-2.5">
          <div className="w-7 h-7 rounded-lg bg-gradient-to-br from-green-500 to-emerald-600 flex items-center justify-center text-white font-bold text-sm shadow">
            R
          </div>
          <div>
            <span className="font-semibold text-slate-900 text-sm">Raftly</span>
            <span className="text-slate-400 text-xs ml-2">Raft consensus · chaos engineering</span>
          </div>
        </div>
        <div className="flex items-center gap-4">
          <a
            href="https://github.com/ani03sha/raftly"
            target="_blank"
            rel="noopener noreferrer"
            className="text-xs text-slate-400 hover:text-slate-700 transition"
          >
            GitHub ↗
          </a>
          <div className="flex items-center gap-1.5 text-xs px-2.5 py-1 rounded-full bg-slate-100 border border-slate-200">
            <span
              className={`w-1.5 h-1.5 rounded-full ${
                connected ? 'bg-green-500' : 'bg-slate-400'
              }`}
            />
            <span className="text-slate-600 font-medium">
              {connected ? 'Live' : 'Connecting…'}
            </span>
          </div>
        </div>
      </header>

      {/* ── Three-column body ── */}
      <div className="flex-1 flex overflow-hidden">
        {/* Left — cluster state (fixed, compact) */}
        <div className="w-[360px] flex-none border-r border-slate-200 overflow-y-auto bg-slate-50">
          <ClusterColumn cluster={cluster} />
        </div>

        {/* Center — scenarios / chaos / KV (flex-1, most space) */}
        <div className="flex-1 border-r border-slate-200 overflow-y-auto bg-white">
          <ControlColumn cluster={cluster} onLocalEvent={pushLocal} />
        </div>

        {/* Right — event timeline (fixed) */}
        <div className="w-[260px] flex-none overflow-y-auto bg-white">
          <EventFeed events={events} localEvents={localEvents} />
        </div>
      </div>
    </div>
  )
}
