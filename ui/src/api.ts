import type {
  ClusterStatus,
  ClusterEvent,
  ChaosState,
  ClusterLogView,
  ClusterConfig,
} from './types'

export async function getCluster(): Promise<ClusterStatus> {
  const res = await fetch('/api/cluster')
  if (!res.ok) throw new Error(`cluster fetch failed: ${res.status}`)
  return res.json()
}

export async function putKey(key: string, value: string): Promise<void> {
  const res = await fetch(`/keys/${encodeURIComponent(key)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ value }),
  })
  if (!res.ok) throw new Error((await res.text()) || `PUT failed: ${res.status}`)
}

export async function getKey(key: string): Promise<string> {
  const res = await fetch(`/keys/${encodeURIComponent(key)}`)
  if (!res.ok) throw new Error((await res.text()) || `GET failed: ${res.status}`)
  const data = await res.json()
  return data.value
}

export async function deleteKey(key: string): Promise<void> {
  const res = await fetch(`/keys/${encodeURIComponent(key)}`, { method: 'DELETE' })
  if (!res.ok) throw new Error((await res.text()) || `DELETE failed: ${res.status}`)
}

export async function getChaosState(): Promise<ChaosState> {
  const res = await fetch('/api/cluster/chaos/state')
  if (!res.ok) throw new Error(`chaos state fetch failed: ${res.status}`)
  return res.json()
}

export async function isolateNode(node: string): Promise<void> {
  await postJSON('/api/cluster/chaos/isolate', { node })
}

export async function partition(a: string, b: string): Promise<void> {
  await postJSON('/api/cluster/chaos/partition', { a, b })
}

export async function injectDelay(node: string, delayMs: number, jitterMs: number): Promise<void> {
  await postJSON('/api/cluster/chaos/delay', { node, delay_ms: delayMs, jitter_ms: jitterMs })
}

export async function injectLoss(node: string, lossRate: number): Promise<void> {
  await postJSON('/api/cluster/chaos/loss', { node, loss_rate: lossRate })
}

export async function healAll(): Promise<void> {
  await postJSON('/api/cluster/chaos/heal', {})
}

export async function getClusterConfig(): Promise<ClusterConfig> {
  const res = await fetch('/api/cluster/config')
  if (!res.ok) throw new Error(`config fetch failed: ${res.status}`)
  return res.json()
}

export async function applyClusterConfig(electionTimeoutMs: number, heartbeatMs: number): Promise<ClusterConfig> {
  const res = await fetch('/api/cluster/config', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ election_timeout_ms: electionTimeoutMs, heartbeat_ms: heartbeatMs }),
  })
  if (!res.ok) throw new Error((await res.text()) || `config update failed: ${res.status}`)
  return res.json()
}

export async function getClusterLog(limit = 20): Promise<ClusterLogView> {
  const res = await fetch(`/api/cluster/log?limit=${limit}`)
  if (!res.ok) throw new Error(`log fetch failed: ${res.status}`)
  return res.json()
}

async function postJSON(url: string, body: unknown): Promise<unknown> {
  const res = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const txt = await res.text().catch(() => '')
    throw new Error(txt || `${url} failed: ${res.status}`)
  }
  return res.json().catch(() => ({}))
}

export function subscribeEvents(
  onEvent: (e: ClusterEvent) => void,
  onOpen?: () => void,
  onError?: () => void,
): () => void {
  const es = new EventSource('/api/events')
  es.onopen = () => onOpen?.()
  es.onerror = () => onError?.()
  es.onmessage = (msg) => {
    try {
      onEvent(JSON.parse(msg.data))
    } catch {
      /* ignore malformed frames */
    }
  }
  return () => es.close()
}
