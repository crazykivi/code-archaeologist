import type { Job, Report, ProvidersResponse, AnalyzeRequest, Provider, ProviderUpdateInput } from '@/types'

const API_BASE = '/api/v1'

export async function analyzeRepository(request: AnalyzeRequest): Promise<{ job_id: string; status: string }> {
  const response = await fetch(`${API_BASE}/analyze`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(request)
  })
  if (!response.ok) {
    const error = await response.json()
    throw new Error(error.error || 'Failed to start analysis')
  }
  return response.json()
}

export async function getJob(id: string): Promise<Job> {
  const response = await fetch(`${API_BASE}/jobs/${id}`)
  if (!response.ok) {
    throw new Error('Job not found')
  }
  return response.json()
}

export async function getReport(id: string): Promise<Report> {
  const response = await fetch(`${API_BASE}/reports/${id}`)
  if (!response.ok) {
    throw new Error('Report not found')
  }
  const markdown = await response.text()
  return {
    id,
    created_at: new Date().toISOString(),
    markdown
  }
}

export async function getProviders(): Promise<ProvidersResponse> {
  const response = await fetch(`${API_BASE}/providers`)
  if (!response.ok) {
    throw new Error('Failed to fetch providers')
  }
  return response.json()
}

export async function listJobs(): Promise<Job[]> {
  const response = await fetch(`${API_BASE}/jobs`)
  if (!response.ok) {
    throw new Error('Failed to list jobs')
  }
  return response.json()
}

export async function deleteJobApi(id: string): Promise<void> {
  const response = await fetch(`${API_BASE}/jobs/${id}`, {
    method: 'DELETE'
  })
  if (!response.ok) {
    let message = 'Failed to delete job'
    try {
      const error = await response.json()
      if (error?.error) message = error.error
    } catch { }
    throw new Error(message)
  }
}

export async function createProvider(name: string, payload: ProviderUpdateInput): Promise<Provider> {
  const response = await fetch(`${API_BASE}/providers/${encodeURIComponent(name)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  })
  return parseProviderResponse(response, 'Не удалось создать провайдера')
}

export async function updateProvider(name: string, payload: ProviderUpdateInput): Promise<Provider> {
  const response = await fetch(`${API_BASE}/providers/${encodeURIComponent(name)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload)
  })
  return parseProviderResponse(response, 'Не удалось сохранить настройки')
}

export async function deleteProvider(name: string): Promise<void> {
  const response = await fetch(`${API_BASE}/providers/${encodeURIComponent(name)}`, {
    method: 'DELETE'
  })
  if (!response.ok) {
    let message = 'Не удалось удалить настройки'
    try {
      const error = await response.json()
      if (error?.error) message = error.error
    } catch { }
    throw new Error(message)
  }
}

export async function testProvider(
  name: string
): Promise<{ ok: boolean; reply?: string; error?: string }> {
  const response = await fetch(`${API_BASE}/providers/${encodeURIComponent(name)}/test`, {
    method: 'POST'
  })
  if (!response.ok) {
    throw new Error('Не удалось выполнить проверку')
  }
  return response.json()
}

async function parseProviderResponse(response: Response, fallback: string): Promise<Provider> {
  if (!response.ok) {
    let message = fallback
    try {
      const error = await response.json()
      if (error?.error) message = error.error
    } catch { }
    throw new Error(message)
  }
  return response.json()
}