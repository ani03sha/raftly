import { useEffect, useState } from 'react'
import type { LocalEvent } from '../types'
import { applyClusterConfig, getClusterConfig } from '../api'

interface Props {
  healDelay: number
  onHealDelayChange: (ms: number) => void
  onLocalEvent: (e: LocalEvent) => void
}

const DEFAULTS = { electionMs: 150, heartbeatMs: 50 }

export default function SettingsPanel({ healDelay, onHealDelayChange, onLocalEvent }: Props) {
  const [electionMs, setElectionMs] = useState(DEFAULTS.electionMs)
  const [heartbeatMs, setHeartbeatMs] = useState(DEFAULTS.heartbeatMs)
  const [busy, setBusy] = useState(false)
  const [applied, setApplied] = useState(false)

  // Fetch current cluster config on mount
  useEffect(() => {
    getClusterConfig()
      .then((cfg) => {
        const first = cfg.nodes.find((n) => !n.error)
        if (first) {
          setElectionMs(first.election_timeout_ms)
          setHeartbeatMs(first.heartbeat_ms)
        }
      })
      .catch(() => {})
  }, [])

  async function apply() {
    setBusy(true)
    setApplied(false)
    try {
      await applyClusterConfig(electionMs, heartbeatMs)
      setApplied(true)
      onLocalEvent({
        id: `${Date.now()}-config`,
        timestamp: Date.now(),
        kind: 'scenario',
        title: 'Config updated',
        detail: `election=${electionMs}ms  heartbeat=${heartbeatMs}ms`,
      })
      setTimeout(() => setApplied(false), 2000)
    } catch (err) {
      onLocalEvent({
        id: `${Date.now()}-config-err`,
        timestamp: Date.now(),
        kind: 'chaos',
        title: 'Config update failed',
        detail: err instanceof Error ? err.message : String(err),
      })
    } finally {
      setBusy(false)
    }
  }

  function reset() {
    setElectionMs(DEFAULTS.electionMs)
    setHeartbeatMs(DEFAULTS.heartbeatMs)
  }

  return (
    <div className="flex flex-col h-full overflow-y-auto">
      <div className="px-5 py-4 space-y-6">

        {/* Timing */}
        <section>
          <SectionLabel>Cluster timing</SectionLabel>
          <p className="text-xs text-slate-500 mb-4 leading-relaxed">
            Changes apply to all nodes instantly — no restart needed. Heartbeat fires every tick;
            election timeout is randomised in <span className="font-mono">[T, 2T)</span> on each reset.
          </p>

          <div className="space-y-5">
            <SliderField
              label="Election timeout"
              value={electionMs}
              onChange={setElectionMs}
              min={50} max={2000} step={10}
              unit="ms"
              hint={electionMs < 100 ? 'Very aggressive — expect frequent elections' :
                    electionMs > 800 ? 'Very conservative — slow to detect failures' :
                    'Typical Raft range'}
              hintColor={electionMs < 100 || electionMs > 800 ? 'text-amber-600' : 'text-green-600'}
            />

            <SliderField
              label="Heartbeat interval"
              value={heartbeatMs}
              onChange={setHeartbeatMs}
              min={10} max={500} step={5}
              unit="ms"
              hint={heartbeatMs >= electionMs
                ? 'Must be less than election timeout!'
                : heartbeatMs > electionMs / 3
                ? 'Close to election timeout — leader may be challenged'
                : 'Good ratio'}
              hintColor={heartbeatMs >= electionMs ? 'text-red-600' :
                         heartbeatMs > electionMs / 3 ? 'text-amber-600' : 'text-green-600'}
            />
          </div>

          <div className="flex items-center gap-2 mt-4">
            <button
              disabled={busy || heartbeatMs >= electionMs}
              onClick={apply}
              className={`flex-1 py-2 rounded-lg text-sm font-semibold transition shadow-sm
                disabled:opacity-40 disabled:cursor-not-allowed
                ${applied ? 'bg-emerald-600 text-white' : 'bg-indigo-600 hover:bg-indigo-700 text-white'}`}
            >
              {busy ? 'Applying…' : applied ? 'Applied ✓' : 'Apply to all nodes'}
            </button>
            <button
              onClick={reset}
              className="px-3 py-2 rounded-lg text-sm text-slate-500 hover:text-slate-800 border border-slate-200 hover:border-slate-400 transition"
            >
              Reset
            </button>
          </div>
        </section>

        {/* Scenario timing */}
        <section>
          <SectionLabel>Scenario auto-heal delay</SectionLabel>
          <p className="text-xs text-slate-500 mb-4 leading-relaxed">
            How long scenarios wait before healing chaos. Increase this to give more time to observe
            leader elections and log replication in action.
          </p>

          <SliderField
            label="Auto-heal after"
            value={Math.round(healDelay / 1000)}
            onChange={(s) => onHealDelayChange(s * 1000)}
            min={2} max={30} step={1}
            unit="s"
            hint={`Scenarios will heal after ${Math.round(healDelay / 1000)}s`}
            hintColor="text-slate-500"
          />
        </section>

        {/* Reference card */}
        <section>
          <SectionLabel>Quick reference</SectionLabel>
          <div className="rounded-lg border border-slate-200 bg-slate-50 divide-y divide-slate-200 text-xs font-mono">
            <Row k="Default election timeout" v="150 ms" />
            <Row k="Default heartbeat" v="50 ms" />
            <Row k="Ratio rule of thumb" v="hb ≤ T / 5" />
            <Row k="Min for stable leader" v="election > 100 ms" />
            <Row k="Election window" v="[T, 2T) randomised" />
          </div>
        </section>

      </div>
    </div>
  )
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="text-[10px] uppercase tracking-widest text-slate-400 font-semibold mb-3">
      {children}
    </div>
  )
}

function SliderField({ label, value, onChange, min, max, step, unit, hint, hintColor }: {
  label: string
  value: number
  onChange: (v: number) => void
  min: number
  max: number
  step: number
  unit: string
  hint: string
  hintColor: string
}) {
  return (
    <div>
      <div className="flex items-baseline justify-between mb-1">
        <label className="text-xs font-semibold text-slate-700">{label}</label>
        <span className="text-sm font-mono font-bold text-slate-900">{value}<span className="text-slate-400 text-xs ml-0.5">{unit}</span></span>
      </div>
      <input
        type="range" min={min} max={max} step={step} value={value}
        onChange={(e) => onChange(parseInt(e.target.value, 10))}
        className="w-full accent-indigo-600"
      />
      <div className="flex justify-between mt-0.5">
        <span className={`text-[10px] ${hintColor}`}>{hint}</span>
        <span className="text-[10px] text-slate-400">{min}–{max}{unit}</span>
      </div>
    </div>
  )
}

function Row({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex justify-between px-3 py-1.5">
      <span className="text-slate-500">{k}</span>
      <span className="text-slate-800 font-semibold">{v}</span>
    </div>
  )
}
