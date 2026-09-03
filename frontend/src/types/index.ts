export type ReportType = 'decisions' | 'architecture' | 'tech_debt' | 'team'

export interface Job {
  id: string
  status: 'queued' | 'running' | 'completed' | 'failed'
  created_at: string
  started_at: string
  finished_at: string
  request: {
    source_type: string
    source: string
    provider: string
    model?: string
    limit: number
    language: string
    cascade: boolean
    diff: boolean
    report_type?: ReportType
    since?: string
    until?: string
    from_commit?: string
    to_commit?: string
  }
  report_id?: string
  error?: string
  progress?: {
    stage: string
    message: string
    details?: string
    total_commits?: number
    processed_items?: number
    total_batches?: number
    done_batches?: number
    total_reduce?: number
    done_reduce?: number
  }
}

export interface Report {
  id: string
  created_at: string
  markdown: string
}

export interface Provider {
  name: string
  configured: boolean
}

export interface ProvidersResponse {
  default: string
  providers: Provider[]
}

export interface AnalyzeRequest {
  source_type: 'local' | 'github' | 'gitlab'
  source: string
  provider?: string
  model?: string
  limit?: number
  language?: string
  cascade?: boolean
  diff?: boolean
  report_type?: ReportType
  since?: string
  until?: string
  from_commit?: string
  to_commit?: string
}