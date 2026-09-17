export type KnowledgePollStatus = {
  parse_status?: string
  summary_status?: string
  agent_build_status?: string
}

export function isKnowledgeParseInFlight(status?: string): boolean {
  return status === 'pending' || status === 'processing' || status === 'finalizing'
}

// isWikiBuildInFlight reports whether an external agent-gateway wiki build is
// still queued/running for an otherwise parse-completed document.
export function isWikiBuildInFlight(status?: string): boolean {
  return status === 'queued' || status === 'running'
}

export function knowledgeNeedsStatusPolling(item: KnowledgePollStatus): boolean {
  if (isKnowledgeParseInFlight(item.parse_status)) return true
  if (item.parse_status === 'completed' && isWikiBuildInFlight(item.agent_build_status)) return true
  return item.parse_status === 'completed' &&
    (item.summary_status === 'pending' || item.summary_status === 'processing')
}

export function shouldRefreshWikiStatusAfterKnowledgePoll(
  before: KnowledgePollStatus,
  after: KnowledgePollStatus,
): boolean {
  return knowledgeNeedsStatusPolling(before) && !knowledgeNeedsStatusPolling(after)
}
