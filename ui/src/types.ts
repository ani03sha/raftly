export type NodeState = 'Leader' | 'Follower' | 'Candidate' | 'PreCandidate' | 'Down' | 'Unknown'

export interface NodeStatus {
  id: string
  state: NodeState
  term: number
  leader_id: string
  commit_index: number
  last_applied: number
  reachable: boolean
}

export interface ClusterStatus {
  nodes: NodeStatus[]
  leader_id: string
  term: number
  timestamp: number
}

export type EventType =
  | 'status'
  | 'leader_change'
  | 'term_change'
  | 'node_down'
  | 'node_up'

export interface ClusterEvent {
  type: EventType
  timestamp: number
  node_id?: string
  data: unknown
}

export type ChaosAction = 'drop' | 'delay' | 'loss' | 'pass'

export interface ChaosRule {
  id: string
  from_node?: string
  to_node?: string
  action: ChaosAction
  params?: Record<string, unknown>
}

export interface ChaosState {
  peers: { node: string; rules: ChaosRule[]; error?: string }[]
}

export interface LogEntryView {
  index: number
  term: number
  type: string
  committed: boolean
  applied: boolean
  data?: string
}

export interface NodeLogView {
  node: string
  last_index: number
  commit_index: number
  last_applied: number
  entries: LogEntryView[]
  error?: string
}

export interface ClusterLogView {
  nodes: NodeLogView[]
}

export interface NodeConfig {
  node: string
  election_timeout_ms: number
  heartbeat_ms: number
  error?: string
}

export interface ClusterConfig {
  nodes: NodeConfig[]
}

export interface LocalEvent {
  id: string
  timestamp: number
  kind: 'chaos' | 'kv' | 'scenario'
  title: string
  detail?: string
}
